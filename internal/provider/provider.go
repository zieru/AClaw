package provider

import (
	"context"
	"fmt"
	"sync"
	"time"

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

// ChatRequest holds parameters for calling an LLM
type ChatRequest struct {
	Model       string
	Messages    []ChatMessage
	Tools       []tools.Tool
	Temperature float64
	MaxTokens   int
}

// ChatResponse holds the output from an LLM call
type ChatResponse struct {
	Content          string
	ToolCalls        []ToolCall
	PromptTokens     int
	CompletionTokens int
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
	GenerateChat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// Manager coordinates multiple providers, selection, and fallbacks
type Manager struct {
	mu        sync.RWMutex
	providers map[string]Provider
	order     []string // Priority order
}

var globalManager *Manager
var managerOnce sync.Once

// GetManager returns the singleton provider manager
func GetManager() *Manager {
	managerOnce.Do(func() {
		globalManager = &Manager{
			providers: make(map[string]Provider),
		}
	})
	return globalManager
}

// Register adds or updates a provider
func (m *Manager) Register(p Provider, priority int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[p.Name()] = p
	// Recompute order if needed
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

// GenerateWithFallback tries the preferred provider first, and falls back if error
func (m *Manager) GenerateWithFallback(ctx context.Context, preferredName string, req ChatRequest) (*ChatResponse, error) {
	m.mu.RLock()
	providersList := make([]Provider, 0, len(m.providers))
	if pref, ok := m.providers[preferredName]; ok {
		providersList = append(providersList, pref)
	}
	for _, p := range m.providers {
		if p.Name() != preferredName {
			providersList = append(providersList, p)
		}
	}
	m.mu.RUnlock()

	if len(providersList) == 0 {
		return nil, fmt.Errorf("tidak ada provider AI yang aktif atau terdaftar")
	}

	var lastErr error
	for _, p := range providersList {
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
