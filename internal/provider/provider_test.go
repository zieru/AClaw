package provider

import (
	"context"
	"fmt"
	"testing"

	"goassistant/internal/storage"
)

type mockProvider struct {
	name         string
	pType        string
	defaultModel string
	models       []string
	failCount    int
	calls        int
	keyPool      *KeyPool
}

func (m *mockProvider) Name() string         { return m.name }
func (m *mockProvider) Type() string         { return m.pType }
func (m *mockProvider) DefaultModel() string { return m.defaultModel }
func (m *mockProvider) Models() []string     { return m.models }
func (m *mockProvider) SetHTTPClient(client interface{}) {}

func (m *mockProvider) GenerateChat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	m.calls++
	if m.calls <= m.failCount {
		return nil, fmt.Errorf("mock error (attempt %d)", m.calls)
	}
	return &ChatResponse{
		Content:          fmt.Sprintf("response from %s (%s)", m.name, req.Model),
		PromptTokens:     10,
		CompletionTokens: 10,
	}, nil
}

func TestKeyPoolRotation(t *testing.T) {
	keys := []string{"key-1", "key-2", "key-3"}
	pool := NewKeyPool(keys, "round-robin")

	k1 := pool.GetNextKey()
	k2 := pool.GetNextKey()
	k3 := pool.GetNextKey()

	if k1 == k2 || k2 == k3 {
		t.Errorf("expected round robin keys, got %s, %s, %s", k1, k2, k3)
	}

	// Test cooldown
	pool.MarkError(k1, true)
	for i := 0; i < 5; i++ {
		k := pool.GetNextKey()
		if k == k1 {
			t.Errorf("expected key in cooldown to be skipped, got %s", k)
		}
	}
}

func TestSmartFallbackAndCombos(t *testing.T) {
	mgr := &Manager{
		providers: make(map[string]Provider),
		combos:    make(map[string]*storage.ModelComboRecord),
	}

	p1 := &mockProvider{
		name:         "openai",
		pType:        "openai",
		defaultModel: "gpt-4o-mini",
		models:       []string{"gpt-4o", "gpt-4o-mini"},
		failCount:    1, // will fail on first call
	}
	p2 := &mockProvider{
		name:         "anthropic",
		pType:        "anthropic",
		defaultModel: "claude-3-5-sonnet",
		models:       []string{"claude-3-5-sonnet"},
		failCount:    0,
	}

	mgr.Register(p1, 1)
	mgr.Register(p2, 2)

	// 1. Test Failsafe fallback across providers
	resp, err := mgr.GenerateWithFallback(context.Background(), "openai", ChatRequest{
		Model: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}
	if resp == nil || resp.Content != "response from anthropic (gpt-4o-mini)" {
		t.Errorf("unexpected response text: %v", resp)
	}

	// 2. Test Combo resolution
	mgr.RegisterCombo(&storage.ModelComboRecord{
		Name: "smart",
		Targets: []storage.ComboTarget{
			{ProviderID: "openai", Model: "gpt-4o"},
			{ProviderID: "anthropic", Model: "claude-3-5-sonnet"},
		},
		Strategy: "failsafe",
		IsActive: true,
	})

	// Reset mock calls
	p1.calls = 0
	p1.failCount = 1 // openai will fail on combo call 1, then fallback to anthropic

	respCombo, err := mgr.GenerateWithFallback(context.Background(), "", ChatRequest{
		Model: "combo:smart",
	})
	if err != nil {
		t.Fatalf("expected combo to succeed, got: %v", err)
	}
	if respCombo == nil || respCombo.Content != "response from anthropic (claude-3-5-sonnet)" {
		t.Errorf("unexpected combo response: %v", respCombo)
	}
}

func TestKeyPoolFailoverStrategy(t *testing.T) {
	keys := []string{"key-pri-1", "key-sec-2", "key-ter-3"}
	pool := NewKeyPool(keys, "failover")

	// Failover should always return key-pri-1 when healthy
	for i := 0; i < 3; i++ {
		k := pool.GetNextKey()
		if k != "key-pri-1" {
			t.Errorf("expected key-pri-1, got %s", k)
		}
	}

	// Mark key-pri-1 with timeout
	pool.MarkTimeout("key-pri-1")
	k := pool.GetNextKey()
	if k != "key-sec-2" {
		t.Errorf("expected failover to key-sec-2, got %s", k)
	}

	// Mark key-sec-2 with rate limit
	pool.MarkRateLimit("key-sec-2")
	k = pool.GetNextKey()
	if k != "key-ter-3" {
		t.Errorf("expected failover to key-ter-3, got %s", k)
	}

	// Mark key-pri-1 success (re-enables it)
	pool.MarkSuccess("key-pri-1")
	k = pool.GetNextKey()
	if k != "key-pri-1" {
		t.Errorf("expected recovery back to key-pri-1, got %s", k)
	}
}

func TestKeyPoolAllInCooldownRecovery(t *testing.T) {
	keys := []string{"k1", "k2"}
	pool := NewKeyPool(keys, "round-robin")

	pool.MarkTimeout("k1")
	pool.MarkRateLimit("k2")

	// Even though all keys are in cooldown, pool should return the earliest available key rather than empty
	k := pool.GetNextKey()
	if k == "" {
		t.Errorf("expected pool to return fallback key when all in cooldown, got empty string")
	}
	if k != "k1" { // k1 has 30s timeout cooldown, k2 has 60s rate limit cooldown
		t.Errorf("expected earliest expiring key k1, got %s", k)
	}
}

