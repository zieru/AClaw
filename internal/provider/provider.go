package provider

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
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
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ChatMessage represents a single message in the LLM conversation
type ChatMessage struct {
	Role       MessageRole `json:"role"`
	Content    string      `json:"content"`
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

// Manager coordinates multiple providers, selection, model routing, combos, and fallbacks
type Manager struct {
	mu            sync.RWMutex
	providers     map[string]Provider
	order         []string // Priority order
	combos        map[string]*storage.ModelComboRecord
	defaultClient interface{}
}

var globalManager *Manager
var managerOnce sync.Once

// GetManager returns the singleton provider manager
func GetManager() *Manager {
	managerOnce.Do(func() {
		globalManager = &Manager{
			providers: make(map[string]Provider),
			combos:    make(map[string]*storage.ModelComboRecord),
		}
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

// Register adds or updates a provider
func (m *Manager) Register(p Provider, priority int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.defaultClient != nil {
		if setter, ok := p.(interface{ SetHTTPClient(interface{}) }); ok {
			setter.SetHTTPClient(m.defaultClient)
		}
	}
	m.providers[p.Name()] = p
	m.updateOrderLocked()
}

// Unregister removes a provider
func (m *Manager) Unregister(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.providers, name)
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
	defer m.mu.Unlock()
	m.combos[strings.ToLower(c.Name)] = c
}

// UnregisterCombo removes a combo
func (m *Manager) UnregisterCombo(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.combos, strings.ToLower(name))
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

// Get finds a provider by name (exact, case-insensitive, type, or substring)
func (m *Manager) Get(name string) (Provider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, false
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

	// 4. Substring / Prefix match in provider name
	nameLower := strings.ToLower(name)
	for _, p := range m.providers {
		if strings.Contains(strings.ToLower(p.Name()), nameLower) {
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
		var attemptErrors []string
		for idx, target := range combo.Targets {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			p, exists := m.providers[target.ProviderID]
			if !exists {
				// Try case-insensitive lookup
				for _, prov := range m.providers {
					if strings.EqualFold(prov.Name(), target.ProviderID) {
						p = prov
						exists = true
						break
					}
				}
			}
			if !exists {
				errMsg := fmt.Sprintf("[%d/%d %s/%s]: provider tidak terdaftar/nonaktif", idx+1, len(combo.Targets), target.ProviderID, target.Model)
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
			resp, err := p.GenerateChat(ctx, targetReq)
			if err == nil && resp != nil {
				if idx > 0 {
					log.Printf("[Combo:%s] Berhasil fallback ke target #%d [%s/%s] (latensi: %dms)", combo.Name, idx+1, target.ProviderID, target.Model, time.Since(targetStart).Milliseconds())
				}
				return resp, nil
			}

			latency := time.Since(targetStart).Milliseconds()
			errItem := fmt.Sprintf("[%d/%d %s/%s (%dms)]: %v", idx+1, len(combo.Targets), target.ProviderID, target.Model, latency, err)
			attemptErrors = append(attemptErrors, errItem)
			log.Printf("[Combo:%s] Target #%d [%s/%s] gagal (%dms): %v", combo.Name, idx+1, target.ProviderID, target.Model, latency, err)
		}
		if len(attemptErrors) > 0 {
			return nil, fmt.Errorf("combo '%s' seluruh target gagal (%d/%d): %s", combo.Name, len(attemptErrors), len(combo.Targets), strings.Join(attemptErrors, " | "))
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
		resp, err := p.GenerateChat(ctx, req)
		if err == nil && resp != nil {
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
