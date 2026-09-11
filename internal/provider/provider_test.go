package provider

import (
	"context"
	"fmt"
	"strings"
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

	// 1. Test Strict non-combo: should fail if the selected provider fails without combo
	_, err := mgr.GenerateWithFallback(context.Background(), "openai", ChatRequest{
		Model: "gpt-4o-mini",
	})
	if err == nil {
		t.Fatalf("expected non-combo failure without fallback, but got success")
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

func TestComboAllTargetsFailDetailedError(t *testing.T) {
	mgr := &Manager{
		providers: make(map[string]Provider),
		combos:    make(map[string]*storage.ModelComboRecord),
	}

	p1 := &mockProvider{
		name:         "provider_a",
		pType:        "openai",
		defaultModel: "model-a",
		models:       []string{"model-a"},
		failCount:    99,
	}
	p2 := &mockProvider{
		name:         "provider_b",
		pType:        "openai",
		defaultModel: "model-b",
		models:       []string{"model-b"},
		failCount:    99,
	}

	mgr.Register(p1, 1)
	mgr.Register(p2, 2)

	mgr.RegisterCombo(&storage.ModelComboRecord{
		Name: "combo_test",
		Targets: []storage.ComboTarget{
			{ProviderID: "provider_a", Model: "model-a"},
			{ProviderID: "provider_b", Model: "model-b"},
		},
		Strategy: "failsafe",
		IsActive: true,
	})

	_, err := mgr.GenerateWithFallback(context.Background(), "", ChatRequest{
		Model: "combo:combo_test",
	})
	if err == nil {
		t.Fatalf("expected error when all targets fail")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "seluruh target gagal (2/2)") {
		t.Errorf("expected count (2/2) in error, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "provider_a/model-a") || !strings.Contains(errMsg, "provider_b/model-b") {
		t.Errorf("expected both providers listed in error message, got: %s", errMsg)
	}
}

func TestComboStrategies(t *testing.T) {
	mgr := &Manager{
		providers:     make(map[string]Provider),
		combos:        make(map[string]*storage.ModelComboRecord),
		comboCounters: make(map[string]*uint64),
		comboLatency:  make(map[string]map[string]*comboLatencyEntry),
	}

	cTargets := []storage.ComboTarget{
		{ProviderID: "p1", Model: "m1"},
		{ProviderID: "p2", Model: "m2"},
		{ProviderID: "p3", Model: "m3"},
	}

	// 1. Failsafe
	cFS := &storage.ModelComboRecord{
		Name:     "c_fs",
		Targets:  cTargets,
		Strategy: "failsafe",
	}
	resFS := mgr.GetOrderedTargets(cFS)
	if len(resFS) != 3 || resFS[0].ProviderID != "p1" || resFS[1].ProviderID != "p2" || resFS[2].ProviderID != "p3" {
		t.Errorf("failsafe unexpected order: %+v", resFS)
	}

	// 2. Round-Robin
	cRR := &storage.ModelComboRecord{
		Name:     "c_rr",
		Targets:  cTargets,
		Strategy: "round-robin",
	}
	// Call 1 -> starts with p1
	r1 := mgr.GetOrderedTargets(cRR)
	if r1[0].ProviderID != "p1" {
		t.Errorf("round-robin call 1 expected p1, got %s", r1[0].ProviderID)
	}
	// Call 2 -> starts with p2
	r2 := mgr.GetOrderedTargets(cRR)
	if r2[0].ProviderID != "p2" {
		t.Errorf("round-robin call 2 expected p2, got %s", r2[0].ProviderID)
	}
	// Call 3 -> starts with p3
	r3 := mgr.GetOrderedTargets(cRR)
	if r3[0].ProviderID != "p3" {
		t.Errorf("round-robin call 3 expected p3, got %s", r3[0].ProviderID)
	}
	// Call 4 -> wraps around to p1
	r4 := mgr.GetOrderedTargets(cRR)
	if r4[0].ProviderID != "p1" {
		t.Errorf("round-robin call 4 expected p1, got %s", r4[0].ProviderID)
	}

	// 3. Race-Probe (Fastest)
	cFast := &storage.ModelComboRecord{
		Name:     "c_fast",
		Targets:  cTargets,
		Strategy: "race-probe",
	}
	// Record latencies: p1 = 800ms, p2 = 120ms (fastest), p3 = 350ms
	mgr.recordComboLatency("c_fast", "p1", "m1", 800*1000*1000, false, "")
	mgr.recordComboLatency("c_fast", "p2", "m2", 120*1000*1000, false, "")
	mgr.recordComboLatency("c_fast", "p3", "m3", 350*1000*1000, false, "")

	resFast := mgr.GetOrderedTargets(cFast)
	if resFast[0].ProviderID != "p2" {
		t.Errorf("race-probe expected fastest p2 first, got %s", resFast[0].ProviderID)
	}
	if resFast[1].ProviderID != "p3" {
		t.Errorf("race-probe expected p3 second, got %s", resFast[1].ProviderID)
	}
	if resFast[2].ProviderID != "p1" {
		t.Errorf("race-probe expected p1 third, got %s", resFast[2].ProviderID)
	}

	// Now simulate p2 failing
	mgr.recordComboLatency("c_fast", "p2", "m2", 5000*1000*1000, true, "timeout")
	resFastFail := mgr.GetOrderedTargets(cFast)
	if resFastFail[0].ProviderID != "p3" {
		t.Errorf("race-probe expected p3 first when p2 fails, got %s", resFastFail[0].ProviderID)
	}
	if resFastFail[2].ProviderID != "p2" {
		t.Errorf("race-probe expected failed p2 last, got %s", resFastFail[2].ProviderID)
	}
}

func TestDahlProviderErrorFormatting(t *testing.T) {
	p := NewOpenAIProvider("dahl", "dahl", "", "dahl-key-123", "")
	if p.baseURL != "https://inference.dahl.global/v1" {
		t.Errorf("expected dahl baseURL 'https://inference.dahl.global/v1', got '%s'", p.baseURL)
	}
	if p.defaultModel != "MiniMaxAI/MiniMax-M2.7" {
		t.Errorf("expected dahl defaultModel 'MiniMaxAI/MiniMax-M2.7', got '%s'", p.defaultModel)
	}

	// 401 Missing API Token
	err401Missing := p.formatHTTPError(401, []byte(`{"error":"unauthorized"}`), "")
	if !strings.Contains(err401Missing.Error(), "Missing API token") || !strings.Contains(err401Missing.Error(), "Authorization: Bearer") {
		t.Errorf("unexpected 401 missing token error message: %v", err401Missing)
	}

	// 401 Invalid/Expired Key
	err401Expired := p.formatHTTPError(401, []byte(`{"error":"invalid token"}`), "dahl-key-123")
	if !strings.Contains(err401Expired.Error(), "Invalid/Expired") || !strings.Contains(err401Expired.Error(), "https://inference.dahl.global/account") {
		t.Errorf("unexpected 401 invalid token error message: %v", err401Expired)
	}

	// 402 Exhausted
	err402 := p.formatHTTPError(402, []byte(`{"error":"insufficient quota"}`), "dahl-key-123")
	if !strings.Contains(err402.Error(), "Exhausted") || !strings.Contains(err402.Error(), "https://inference.dahl.global/docs/tokens/") {
		t.Errorf("unexpected 402 exhausted error message: %v", err402)
	}

	// 502 Stale Model ID
	err502 := p.formatHTTPError(502, []byte(`{"error":"bad gateway"}`), "dahl-key-123")
	if !strings.Contains(err502.Error(), "Stale model id") || !strings.Contains(err502.Error(), "https://inference.dahl.global/status") {
		t.Errorf("unexpected 502 stale model error message: %v", err502)
	}

	// 404 Model Not Found
	err404 := p.formatHTTPError(404, []byte(`{"error":"model not found"}`), "dahl-key-123")
	if !strings.Contains(err404.Error(), "Stale model id") {
		t.Errorf("unexpected 404 stale model error message: %v", err404)
	}

	// 503 Network Overload
	err503 := p.formatHTTPError(503, []byte(`{"error":"service unavailable"}`), "dahl-key-123")
	if !strings.Contains(err503.Error(), "Network Overload") {
		t.Errorf("unexpected 503 network overload error message: %v", err503)
	}
}

func TestComboPreferredIfAvailable(t *testing.T) {
	mgr := &Manager{
		providers:     make(map[string]Provider),
		providersByID: make(map[string]Provider),
		combos:        make(map[string]*storage.ModelComboRecord),
		comboCounters: make(map[string]*uint64),
		comboLatency:  make(map[string]map[string]*comboLatencyEntry),
	}

	// Provider A supports gemini-2.0-flash (default) and gemini-1.5-pro
	pA := &mockProvider{
		name:         "gemini",
		pType:        "gemini",
		defaultModel: "gemini-2.0-flash",
		models:       []string{"gemini-2.0-flash", "gemini-1.5-pro"},
	}
	mgr.Register(pA, 1)

	comboRec := &storage.ModelComboRecord{
		Name:     "smart_combo",
		Strategy: "failsafe",
		IsActive: true,
		Targets: []storage.ComboTarget{
			{
				ProviderID:           "gemini",
				Model:                "gemini-2.0-flash",
				Priority:             1,
				PreferredIfAvailable: true,
			},
		},
	}
	mgr.RegisterCombo(comboRec)

	// Case 1: Caller requests preferred model "gemini-1.5-pro" which Provider A supports
	reqSupported := ChatRequest{
		Model:          "combo:smart_combo",
		PreferredModel: "gemini-1.5-pro",
		Messages: []ChatMessage{
			{Role: RoleUser, Content: "halo"},
		},
	}
	resp1, err := mgr.GenerateWithFallback(context.Background(), "", reqSupported)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp1.Content, "gemini-1.5-pro") {
		t.Errorf("expected response using preferred model 'gemini-1.5-pro', got '%s'", resp1.Content)
	}

	// Case 2: Caller requests preferred model "claude-3-5-sonnet" which Provider A does NOT support
	reqUnsupported := ChatRequest{
		Model:          "combo:smart_combo",
		PreferredModel: "claude-3-5-sonnet",
		Messages: []ChatMessage{
			{Role: RoleUser, Content: "halo"},
		},
	}
	resp2, err := mgr.GenerateWithFallback(context.Background(), "", reqUnsupported)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp2.Content, "gemini-2.0-flash") {
		t.Errorf("expected response fallback to target model 'gemini-2.0-flash', got '%s'", resp2.Content)
	}
}

func TestComboSkipsDisabledModel(t *testing.T) {
	mgr := &Manager{
		providers: make(map[string]Provider),
		combos:    make(map[string]*storage.ModelComboRecord),
	}

	// Provider 1 has only "model-1-b" enabled ("model-1-a" was disabled)
	p1 := &mockProvider{
		name:         "p1",
		pType:        "openai",
		defaultModel: "model-1-b",
		models:       []string{"model-1-b"},
	}
	// Provider 2 has "model-2-a"
	p2 := &mockProvider{
		name:         "p2",
		pType:        "anthropic",
		defaultModel: "model-2-a",
		models:       []string{"model-2-a"},
	}

	mgr.RegisterWithID("p1", p1, 1)
	mgr.RegisterWithID("p2", p2, 2)

	// Combo targets p1 with disabled "model-1-a", then p2 with "model-2-a"
	mgr.RegisterCombo(&storage.ModelComboRecord{
		Name:     "fallback_combo",
		Strategy: "failsafe",
		IsActive: true,
		Targets: []storage.ComboTarget{
			{ProviderID: "p1", Model: "model-1-a", Priority: 1},
			{ProviderID: "p2", Model: "model-2-a", Priority: 2},
		},
	})

	resp, err := mgr.GenerateWithFallback(context.Background(), "", ChatRequest{
		Model: "combo:fallback_combo",
		Messages: []ChatMessage{
			{Role: RoleUser, Content: "test"},
		},
	})
	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}
	if !strings.Contains(resp.Content, "p2 (model-2-a)") {
		t.Fatalf("expected target #2 response, got: %s", resp.Content)
	}
}

func TestDahlResilientCandidateSelection(t *testing.T) {
	// Dahl provider with only active models
	dahl := NewOpenAIProviderWithKeys(
		"Dahl",
		"dahl",
		"https://inference.dahl.global/v1",
		[]string{"key1"},
		"round-robin",
		"MiniMaxAI/MiniMax-M2.7",
		[]string{"MiniMaxAI/MiniMax-M2.7", "deepseek-ai/DeepSeek-V4-Flash-0731"},
	)

	// Case 1: Default request -> starts with MiniMax, then DeepSeek
	c1 := dahl.getResilientModelCandidates("")
	if len(c1) != 2 || c1[0] != "MiniMaxAI/MiniMax-M2.7" || c1[1] != "deepseek-ai/DeepSeek-V4-Flash-0731" {
		t.Fatalf("unexpected candidates for empty model: %v", c1)
	}

	// Case 2: Specific request for deepseek -> starts with DeepSeek, then MiniMax
	c2 := dahl.getResilientModelCandidates("deepseek-ai/DeepSeek-V4-Flash-0731")
	if len(c2) != 2 || c2[0] != "deepseek-ai/DeepSeek-V4-Flash-0731" || c2[1] != "MiniMaxAI/MiniMax-M2.7" {
		t.Fatalf("unexpected candidates for deepseek request: %v", c2)
	}

	// Case 3: Request for a disabled/unlisted model -> does not include disabled model, uses active ones
	c3 := dahl.getResilientModelCandidates("zai-org/GLM-5.3-Flash")
	if len(c3) != 2 || c3[0] != "MiniMaxAI/MiniMax-M2.7" || c3[1] != "deepseek-ai/DeepSeek-V4-Flash-0731" {
		t.Fatalf("unexpected candidates for disabled model: %v", c3)
	}
}


