package provider

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/storage"
	"goassistant/internal/tools"
)

func defaultAPITimeout() time.Duration {
	if cfg := config.Get(); cfg != nil && cfg.Timeouts.APICallSeconds > 0 {
		return time.Duration(cfg.Timeouts.APICallSeconds) * time.Second
	}
	return 90 * time.Second
}

// MessageRole represents the role of a message
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// ToolCall represents a function call request by the model
type ToolCall struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	Arguments        map[string]interface{} `json:"arguments"`
	ThoughtSignature string                 `json:"thought_signature,omitempty"`
}

// ChatMessage represents a single message in the LLM conversation
type ChatMessage struct {
	Role       MessageRole `json:"role"`
	Content    string      `json:"content"`
	Images     []string    `json:"images,omitempty"` // Base64 data strings or URLs for vision models
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"` // For role=tool response
	Name       string      `json:"name,omitempty"`
}

// StreamChunk represents a single chunk of streamed response
type StreamChunk struct {
	Content  string // Text content delta
	Thinking string // Thinking/reasoning delta
	Done     bool   // True when stream is complete
}

// StreamCallback is called for each chunk during streaming
type StreamCallback func(chunk StreamChunk)

// ChatRequest holds parameters for calling an LLM
type ChatRequest struct {
	Model           string
	Messages        []ChatMessage
	Tools           []tools.Tool
	Temperature     float64
	MaxTokens       int
	Stream          bool           // Enable streaming response
	StreamCallback  StreamCallback // Callback for streaming chunks
	ThinkingEnabled bool           // Enable thinking/reasoning output
	ThinkingBudget  int            // Max tokens for thinking (0 = provider default)
	OnProgress      func(status string) // Optional progress callback during retry attempts
}

// ChatResponse holds the output from an LLM call
type ChatResponse struct {
	Content             string
	Thinking            string // Thinking/reasoning content from model
	ToolCalls           []ToolCall
	PromptTokens        int
	CompletionTokens    int
	ThinkingTokens      int // Tokens used for thinking
	TotalTokens         int
	CacheReadTokens     int // Tokens read from provider prompt cache (50-90% discount)
	CacheCreationTokens int // Tokens written to provider prompt cache
	CostUSD             float64
	Latency             time.Duration
	Tries               int // Number of provider attempts before success
	Model               string
	ProviderName        string
}

// Provider is the interface every AI provider must implement
type Provider interface {
	Name() string
	Type() string
	DefaultModel() string
	Models() []string
	GenerateChat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	SetHTTPClient(client interface{})
}

// StreamingProvider is an optional interface for providers that support streaming
type StreamingProvider interface {
	Provider
	GenerateChatStream(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// IsFreeProvider returns true if the provider type is a free/no-key provider
func IsFreeProvider(providerType string) bool {
	switch strings.ToLower(providerType) {
	case "free_openai", "free_gemini", "free", "opencodefree", "gemini_web", "gemini_scrape":
		return true
	default:
		return false
	}
}

type comboLatencyEntry struct {
	latency   time.Duration
	lastCheck time.Time
	failed    bool
	err       string
}

// Manager coordinates multiple providers, selection, model routing, combos, and fallbacks
type Manager struct {
	mu            sync.RWMutex
	providers     map[string]Provider
	providersByID map[string]Provider // Map lowercase ID -> Provider instance
	order         []string            // Priority order
	combos        map[string]*storage.ModelComboRecord
	defaultClient interface{}

	comboMu       sync.RWMutex
	comboCounters map[string]*uint64
	comboLatency  map[string]map[string]*comboLatencyEntry
	stopProbe     chan struct{}
}

var globalManager *Manager
var managerOnce sync.Once

// GetManager returns the singleton provider manager
func GetManager() *Manager {
	managerOnce.Do(func() {
		globalManager = &Manager{
			providers:     make(map[string]Provider),
			providersByID: make(map[string]Provider),
			combos:        make(map[string]*storage.ModelComboRecord),
			comboCounters: make(map[string]*uint64),
			comboLatency:  make(map[string]map[string]*comboLatencyEntry),
			stopProbe:     make(chan struct{}),
		}
		globalManager.startProber()
	})
	return globalManager
}

// SetDefaultHTTPClient sets the proxy pool or custom HTTP transport client on all providers
func (m *Manager) SetDefaultHTTPClient(client interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultClient = client
	for _, p := range m.providers {
		if setter, ok := p.(interface{ SetHTTPClient(interface{}) }); ok {
			setter.SetHTTPClient(client)
		}
	}
}

// Register adds or updates a provider with optional ID
func (m *Manager) Register(p Provider, priority int) {
	m.RegisterWithID("", p, priority)
}

// RegisterWithID adds or updates a provider associating both its unique ID and Name
func (m *Manager) RegisterWithID(id string, p Provider, priority int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.defaultClient != nil {
		if setter, ok := p.(interface{ SetHTTPClient(interface{}) }); ok {
			setter.SetHTTPClient(m.defaultClient)
		}
	}
	m.providers[p.Name()] = p
	if m.providersByID == nil {
		m.providersByID = make(map[string]Provider)
	}
	if id != "" {
		m.providersByID[strings.ToLower(strings.TrimSpace(id))] = p
	}
	m.updateOrderLocked()
}

// Unregister removes a provider by name or ID
func (m *Manager) Unregister(nameOrID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.providers, nameOrID)
	if m.providersByID != nil {
		delete(m.providersByID, strings.ToLower(strings.TrimSpace(nameOrID)))
	}
	m.updateOrderLocked()
}

func (m *Manager) updateOrderLocked() {
	var list []string
	for k := range m.providers {
		list = append(list, k)
	}
	m.order = list
}

// RegisterCombo registers a multi-provider fallback combo
func (m *Manager) RegisterCombo(c *storage.ModelComboRecord) {
	m.mu.Lock()
	m.combos[strings.ToLower(c.Name)] = c
	strat := strings.ToLower(strings.TrimSpace(c.Strategy))
	m.mu.Unlock()

	if strat == "race-probe" || strat == "fastest" || strat == "race" || strat == "latency" || strat == "best-latency" {
		go m.probeComboTargets(c)
	}
}

// UnregisterCombo removes a combo
func (m *Manager) UnregisterCombo(name string) {
	m.mu.Lock()
	cName := strings.ToLower(name)
	delete(m.combos, cName)
	m.mu.Unlock()

	m.comboMu.Lock()
	delete(m.comboCounters, cName)
	delete(m.comboLatency, cName)
	m.comboMu.Unlock()
}

// GetCombo retrieves a combo by name
func (m *Manager) GetCombo(name string) (*storage.ModelComboRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.combos[strings.ToLower(name)]
	return c, ok
}

// ListCombos returns all registered combos
func (m *Manager) ListCombos() []*storage.ModelComboRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var list []*storage.ModelComboRecord
	for _, c := range m.combos {
		list = append(list, c)
	}
	return list
}

func (m *Manager) recordComboLatency(comboName, providerID, model string, latency time.Duration, failed bool, errStr string) {
	m.comboMu.Lock()
	defer m.comboMu.Unlock()
	comboKey := strings.ToLower(comboName)
	if m.comboLatency == nil {
		m.comboLatency = make(map[string]map[string]*comboLatencyEntry)
	}
	targetMap, ok := m.comboLatency[comboKey]
	if !ok {
		targetMap = make(map[string]*comboLatencyEntry)
		m.comboLatency[comboKey] = targetMap
	}
	targetKey := fmt.Sprintf("%s/%s", strings.ToLower(providerID), strings.ToLower(model))
	targetMap[targetKey] = &comboLatencyEntry{
		latency:   latency,
		lastCheck: time.Now(),
		failed:    failed,
		err:       errStr,
	}
}

// GetOrderedTargets returns targets reordered according to the combo's routing strategy
func (m *Manager) GetOrderedTargets(combo *storage.ModelComboRecord) []storage.ComboTarget {
	if combo == nil || len(combo.Targets) == 0 {
		return nil
	}

	targets := make([]storage.ComboTarget, len(combo.Targets))
	copy(targets, combo.Targets)

	strat := strings.ToLower(strings.TrimSpace(combo.Strategy))
	comboKey := strings.ToLower(combo.Name)

	switch strat {
	case "round-robin", "rr":
		m.comboMu.Lock()
		if m.comboCounters == nil {
			m.comboCounters = make(map[string]*uint64)
		}
		cntPtr, ok := m.comboCounters[comboKey]
		if !ok {
			var initCnt uint64
			cntPtr = &initCnt
			m.comboCounters[comboKey] = cntPtr
		}
		val := atomic.AddUint64(cntPtr, 1)
		m.comboMu.Unlock()

		offset := int((val - 1) % uint64(len(targets)))
		if offset > 0 {
			targets = append(targets[:0], append(targets[offset:], targets[:offset]...)...)
		}
		return targets

	case "random", "rand":
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		r.Shuffle(len(targets), func(i, j int) {
			targets[i], targets[j] = targets[j], targets[i]
		})
		return targets

	case "race-probe", "fastest", "race", "latency", "best-latency":
		m.comboMu.RLock()
		var targetMap map[string]*comboLatencyEntry
		if m.comboLatency != nil {
			targetMap = m.comboLatency[comboKey]
		}
		m.comboMu.RUnlock()

		// If never probed yet, trigger background probe and use default order for this turn
		if len(targetMap) == 0 {
			go m.probeComboTargets(combo)
			return targets
		}

		type targetScore struct {
			target  storage.ComboTarget
			latency time.Duration
		}

		scores := make([]targetScore, len(targets))
		for i, t := range targets {
			tKey := fmt.Sprintf("%s/%s", strings.ToLower(t.ProviderID), strings.ToLower(t.Model))
			entry := targetMap[tKey]
			if entry != nil {
				isRecentFail := entry.failed && time.Since(entry.lastCheck) < 2*time.Minute
				effLatency := entry.latency
				if isRecentFail {
					effLatency = 99999 * time.Millisecond
				}
				scores[i] = targetScore{
					target:  t,
					latency: effLatency,
				}
			} else {
				scores[i] = targetScore{
					target:  t,
					latency: 1500 * time.Millisecond,
				}
			}
		}

		sort.SliceStable(scores, func(i, j int) bool {
			return scores[i].latency < scores[j].latency
		})

		ordered := make([]storage.ComboTarget, len(targets))
		for i, s := range scores {
			ordered[i] = s.target
		}
		return ordered

	default: // "failsafe" or default priority
		return targets
	}
}

func (m *Manager) probeComboTargets(combo *storage.ModelComboRecord) {
	if combo == nil || len(combo.Targets) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, target := range combo.Targets {
		m.mu.RLock()
		p, exists := m.getLocked(target.ProviderID)
		m.mu.RUnlock()

		if !exists || p == nil {
			continue
		}

		wg.Add(1)
		go func(p Provider, t storage.ComboTarget) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()

			req := ChatRequest{
				Model: t.Model,
				Messages: []ChatMessage{
					{
						Role:    RoleUser,
						Content: "ping",
					},
				},
				MaxTokens:   1,
				Temperature: 0.1,
			}

			start := time.Now()
			resp, err := executeProviderCall(probeCtx, p, req)
			dur := time.Since(start)

			failed := (err != nil || resp == nil)
			errStr := ""
			if err != nil {
				errStr = err.Error()
			}
			m.recordComboLatency(combo.Name, t.ProviderID, t.Model, dur, failed, errStr)
			if !failed {
				log.Printf("[Probe:%s] %s/%s respons: %dms", combo.Name, t.ProviderID, t.Model, dur.Milliseconds())
			} else {
				log.Printf("[Probe:%s] %s/%s gagal (%dms): %v", combo.Name, t.ProviderID, t.Model, dur.Milliseconds(), err)
			}
		}(p, target)
	}
	wg.Wait()
}

func (m *Manager) startProber() {
	go func() {
		// Wait 10 seconds on startup for all providers & combos to load
		time.Sleep(10 * time.Second)
		ticker := time.NewTicker(90 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-m.stopProbe:
				return
			case <-ticker.C:
				m.probeActiveCombos()
			}
		}
	}()
}

func (m *Manager) probeActiveCombos() {
	m.mu.RLock()
	var combosToProbe []*storage.ModelComboRecord
	for _, c := range m.combos {
		if c.IsActive && len(c.Targets) > 0 {
			strat := strings.ToLower(strings.TrimSpace(c.Strategy))
			if strat == "race-probe" || strat == "fastest" || strat == "race" || strat == "latency" || strat == "best-latency" {
				combosToProbe = append(combosToProbe, c)
			}
		}
	}
	m.mu.RUnlock()

	for _, c := range combosToProbe {
		m.probeComboTargets(c)
	}
}

// StopProber stops background combo prober
func (m *Manager) StopProber() {
	m.comboMu.Lock()
	defer m.comboMu.Unlock()
	if m.stopProbe != nil {
		select {
		case <-m.stopProbe:
		default:
			close(m.stopProbe)
		}
	}
}

// Get finds a provider by name (exact, case-insensitive, type, or substring)
func (m *Manager) Get(name string) (Provider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getLocked(name)
}

func (m *Manager) getLocked(name string) (Provider, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, false
	}

	nameLower := strings.ToLower(name)

	// 0. Exact ID match from providersByID
	if m.providersByID != nil {
		if p, ok := m.providersByID[nameLower]; ok {
			return p, true
		}
	}

	// 1. Exact match
	if p, ok := m.providers[name]; ok {
		return p, true
	}

	// 2. Case-insensitive exact name match
	for _, p := range m.providers {
		if strings.EqualFold(p.Name(), name) {
			return p, true
		}
	}

	// 3. Match by Type() (e.g. "gemini", "groq", "openai", "anthropic")
	for _, p := range m.providers {
		if strings.EqualFold(p.Type(), name) {
			return p, true
		}
	}

	// 4. Substring / Prefix match in provider name (bidirectional, e.g. "hcnsec" matches "HCNSEC.cn")
	for _, p := range m.providers {
		pNameLower := strings.ToLower(p.Name())
		if strings.Contains(pNameLower, nameLower) || strings.Contains(nameLower, pNameLower) {
			return p, true
		}
	}

	return nil, false
}

// ListAll returns all active registered providers
func (m *Manager) ListAll() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var list []Provider
	for _, name := range m.order {
		if p, ok := m.providers[name]; ok {
			list = append(list, p)
		}
	}
	return list
}

// GenerateWithFallback executes chat with 9Router Smart Routing, Combo resolution, and Failsafe Fallbacks
func (m *Manager) GenerateWithFallback(ctx context.Context, preferredName string, req ChatRequest) (*ChatResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 1. Check if model or preferredName is a registered Combo (e.g. "combo:smart" or "smart")
	comboName := strings.ToLower(strings.TrimPrefix(req.Model, "combo:"))
	if combo, ok := m.combos[comboName]; ok && combo.IsActive && len(combo.Targets) > 0 {
		orderedTargets := m.GetOrderedTargets(combo)
		var attemptErrors []string
		for idx, target := range orderedTargets {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			p, exists := m.getLocked(target.ProviderID)
			if !exists {
				errMsg := fmt.Sprintf("[%d/%d %s/%s]: provider tidak terdaftar/nonaktif", idx+1, len(orderedTargets), target.ProviderID, target.Model)
				attemptErrors = append(attemptErrors, errMsg)
				log.Printf("[Combo:%s] Target #%d [%s/%s] tidak ditemukan atau nonaktif", combo.Name, idx+1, target.ProviderID, target.Model)
				continue
			}

			if idx > 0 && req.OnProgress != nil {
				if idx == 1 {
					req.OnProgress(fmt.Sprintf("⏳ Mencoba target cadangan combo (%s/%s), butuh sedikit waktu...", target.ProviderID, target.Model))
				} else {
					req.OnProgress(fmt.Sprintf("⏳ Percobaan combo ke-%d (%s/%s), masih membutuhkan waktu...", idx+1, target.ProviderID, target.Model))
				}
			}

			targetReq := req
			targetReq.Model = target.Model

			targetStart := time.Now()
			resp, err := executeProviderCall(ctx, p, targetReq)
			targetLatency := time.Since(targetStart)
			if err == nil && resp != nil {
				m.recordComboLatency(combo.Name, target.ProviderID, target.Model, targetLatency, false, "")
				resp.Tries = idx + 1
				if idx > 0 {
					log.Printf("[Combo:%s] Berhasil fallback ke target #%d [%s/%s] (latensi: %dms)", combo.Name, idx+1, target.ProviderID, target.Model, targetLatency.Milliseconds())
				}
				return resp, nil
			}

			m.recordComboLatency(combo.Name, target.ProviderID, target.Model, targetLatency, true, fmt.Sprintf("%v", err))
			latency := targetLatency.Milliseconds()
			errItem := fmt.Sprintf("[%d/%d %s/%s (%dms)]: %v", idx+1, len(orderedTargets), target.ProviderID, target.Model, latency, err)
			attemptErrors = append(attemptErrors, errItem)
			log.Printf("[Combo:%s] Target #%d [%s/%s] gagal (%dms): %v", combo.Name, idx+1, target.ProviderID, target.Model, latency, err)
		}
		if len(attemptErrors) > 0 {
			return nil, fmt.Errorf("combo '%s' seluruh target gagal (%d/%d): %s", combo.Name, len(attemptErrors), len(orderedTargets), strings.Join(attemptErrors, " | "))
		}
	}

	// 2. Build candidate provider list based on requested model and preference
	var primaryCandidates []Provider
	var secondaryCandidates []Provider

	for _, p := range m.providers {
		if req.Model != "" {
			supportsModel := false
			if strings.EqualFold(p.DefaultModel(), req.Model) {
				supportsModel = true
			} else {
				for _, mod := range p.Models() {
					if strings.EqualFold(mod, req.Model) {
						supportsModel = true
						break
					}
				}
			}
			if supportsModel {
				if strings.EqualFold(p.Name(), preferredName) {
					primaryCandidates = append([]Provider{p}, primaryCandidates...)
				} else {
					primaryCandidates = append(primaryCandidates, p)
				}
				continue
			}
		}

		if strings.EqualFold(p.Name(), preferredName) {
			secondaryCandidates = append([]Provider{p}, secondaryCandidates...)
		} else {
			secondaryCandidates = append(secondaryCandidates, p)
		}
	}

	var executionList []Provider
	// Jika user secara eksplisit menentukan single model (bukan combo), HANYA jalankan provider yang mendukung model tersebut
	if req.Model != "" && len(primaryCandidates) > 0 {
		executionList = primaryCandidates
	} else if preferredName != "" && len(secondaryCandidates) > 0 {
		executionList = secondaryCandidates
	} else {
		executionList = append(primaryCandidates, secondaryCandidates...)
	}

	if len(executionList) == 0 {
		if req.Model != "" {
			return nil, fmt.Errorf("tidak ada provider aktif yang mendukung model '%s'", req.Model)
		}
		return nil, fmt.Errorf("tidak ada provider AI yang aktif atau terdaftar")
	}

	var executionErrors []string
	for idx, p := range executionList {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if idx > 0 && req.OnProgress != nil {
			if idx == 1 {
				req.OnProgress(fmt.Sprintf("⏳ Sedang mencoba ulang (%s), butuh sedikit waktu...", p.Name()))
			} else {
				req.OnProgress(fmt.Sprintf("⏳ Percobaan ke-%d (%s), masih membutuhkan waktu...", idx+1, p.Name()))
			}
		}

		pStart := time.Now()
		resp, err := executeProviderCall(ctx, p, req)
		if err == nil && resp != nil {
			resp.Tries = idx + 1
			if idx > 0 {
				log.Printf("[Fallback] Berhasil fallback ke provider #%d [%s] (latensi: %dms)", idx+1, p.Name(), time.Since(pStart).Milliseconds())
			}
			return resp, nil
		}
		latency := time.Since(pStart).Milliseconds()
		executionErrors = append(executionErrors, fmt.Sprintf("[%d/%d %s (%dms)]: %v", idx+1, len(executionList), p.Name(), latency, err))
		log.Printf("[Execution] Provider #%d [%s] gagal (%dms): %v", idx+1, p.Name(), latency, err)
	}

	if len(executionErrors) > 0 {
		return nil, fmt.Errorf("provider AI gagal dieksekusi (%d/%d): %s", len(executionErrors), len(executionList), strings.Join(executionErrors, " | "))
	}
	return nil, fmt.Errorf("semua provider AI gagal dieksekusi")
}

func executeProviderCall(ctx context.Context, p Provider, req ChatRequest) (*ChatResponse, error) {
	var resp *ChatResponse
	var err error
	if req.Stream && req.StreamCallback != nil {
		if sp, ok := p.(StreamingProvider); ok {
			resp, err = sp.GenerateChatStream(ctx, req)
		} else {
			resp, err = p.GenerateChat(ctx, req)
		}
	} else {
		resp, err = p.GenerateChat(ctx, req)
	}
	if err == nil && resp != nil {
		if resp.ProviderName == "" {
			resp.ProviderName = p.Name()
		}
		if resp.Model == "" {
			resp.Model = req.Model
		}
	}
	return resp, err
}
