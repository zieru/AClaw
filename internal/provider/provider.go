package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"goassistant/internal/storage"
	"goassistant/internal/tools"
)

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
}

// ChatResponse holds the output from an LLM call
type ChatResponse struct {
	Content          string
	Thinking         string // Thinking/reasoning content from model
	ToolCalls        []ToolCall
	PromptTokens     int
	CompletionTokens int
	ThinkingTokens   int // Tokens used for thinking
	TotalTokens      int
	CostUSD          float64
	Latency          time.Duration
	Model            string
	ProviderName     string
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

// Get finds a provider by name
func (m *Manager) Get(name string) (Provider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.providers[name]
	return p, ok
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
		var lastErr error
		for _, target := range combo.Targets {
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
				continue
			}

			targetReq := req
			targetReq.Model = target.Model

			resp, err := p.GenerateChat(ctx, targetReq)
			if err == nil && resp != nil {
				return resp, nil
			}
			lastErr = fmt.Errorf("[%s/%s] %w", target.ProviderID, target.Model, err)
		}
		if lastErr != nil {
			return nil, fmt.Errorf("combo '%s' seluruh target gagal: %w", combo.Name, lastErr)
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
	executionList = append(executionList, primaryCandidates...)
	executionList = append(executionList, secondaryCandidates...)

	if len(executionList) == 0 {
		return nil, fmt.Errorf("tidak ada provider AI yang aktif atau terdaftar")
	}

	var lastErr error
	for _, p := range executionList {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		resp, err := p.GenerateChat(ctx, req)
		if err == nil && resp != nil {
			return resp, nil
		}
		lastErr = fmt.Errorf("[%s] %w", p.Name(), err)
	}

	return nil, fmt.Errorf("semua provider AI gagal dieksekusi: %w", lastErr)
}
