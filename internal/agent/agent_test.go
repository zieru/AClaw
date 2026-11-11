package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/memory"
	"goassistant/internal/provider"
	"goassistant/internal/storage"
	"goassistant/internal/tools"
)

func TestMDLoaderAndPromptBuilder(t *testing.T) {
	tempDir := t.TempDir()

	identityContent := "# Custom Identity\nKamu adalah GoAssistant untuk user {{user_name}} di channel {{channel}}."
	err := os.WriteFile(filepath.Join(tempDir, "IDENTITY.md"), []byte(identityContent), 0644)
	if err != nil {
		t.Fatalf("failed to write test identity.md: %v", err)
	}

	loader := NewMDLoader(tempDir)
	promptBld := NewPromptBuilder(loader)

	prompt, err := promptBld.BuildSystemPrompt(PromptContext{
		UserName:    "Budi",
		UserID:      "123",
		ChannelName: "Telegram Dev",
		ChannelType: "telegram",
	})
	if err != nil {
		t.Fatalf("failed to build system prompt: %v", err)
	}

	if !strings.Contains(prompt, "Budi") {
		t.Fatalf("expected template variable {{user_name}} to be replaced with 'Budi', got:\n%s", prompt)
	}

	if !strings.Contains(prompt, "Telegram Dev") {
		t.Fatalf("expected template variable {{channel}} to be replaced with 'Telegram Dev', got:\n%s", prompt)
	}
}

func TestFormatFooter(t *testing.T) {
	// Mode "off"
	offFooter := FormatFooter("off", 100, 50, 0, 150, 0, 500*1000*1000, "gpt-4o", "openai", nil)
	if offFooter != "" {
		t.Fatalf("expected empty footer for mode 'off', got '%s'", offFooter)
	}

	// Mode "tokens"
	tokensFooter := FormatFooter("tokens", 1000, 250, 0, 1250, 200, 500*1000*1000, "gpt-4o", "openai", nil)
	if !strings.Contains(tokensFooter, "🪙 1,250 tokens") || !strings.Contains(tokensFooter, "hemat: 200") {
		t.Fatalf("expected '🪙 1,250 tokens (🌿 hemat: 200)', got '%s'", tokensFooter)
	}

	// Mode "full"
	fullFooter := FormatFooter("full", 1120, 300, 0, 1420, 350, 850*1000*1000, "gpt-4o-mini", "9router", []string{"search_web", "read_db"})
	if !strings.Contains(fullFooter, "⚡ 850ms") {
		t.Errorf("expected latency '⚡ 850ms' in full footer, got '%s'", fullFooter)
	}
	if !strings.Contains(fullFooter, "🪙 1,420 (in: 1,120 / out: 300)") {
		t.Errorf("expected token breakdown in full footer, got '%s'", fullFooter)
	}
	if !strings.Contains(fullFooter, "hemat 350") {
		t.Errorf("expected 'hemat 350' in full footer, got '%s'", fullFooter)
	}
	if !strings.Contains(fullFooter, "🔧 2 tools") {
		t.Errorf("expected tools count in full footer, got '%s'", fullFooter)
	}
	if !strings.Contains(fullFooter, "🤖 9router • gpt-4o-mini") {
		t.Errorf("expected provider and model name in full footer, got '%s'", fullFooter)
	}
}

func TestSubagentPromptAndTool(t *testing.T) {
	tempDir := t.TempDir()
	agentsContent := "### 1. Agent: Coder (@coder)\n- Fokus: Menulis kode Go & debugging.\n### 2. Agent: Researcher (@researcher)\n- Fokus: Riset data."
	_ = os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(agentsContent), 0644)

	loader := NewMDLoader(tempDir)
	pb := NewPromptBuilder(loader)

	// Test Subagent prompt building
	subPrompt, err := pb.BuildSubagentPrompt("coder")
	if err != nil {
		t.Fatalf("unexpected error building subagent prompt: %v", err)
	}
	if !strings.Contains(subPrompt, "CODER") || !strings.Contains(subPrompt, "Menulis kode Go") {
		t.Errorf("expected coder instructions in subagent prompt, got:\n%s", subPrompt)
	}

	// Test Analyst Subagent prompt building
	analystPrompt, err := pb.BuildSubagentPrompt("analyst")
	if err != nil {
		t.Fatalf("unexpected error building analyst prompt: %v", err)
	}
	if !strings.Contains(analystPrompt, "capture_visit_performance") || !strings.Contains(analystPrompt, "Laporan Eksekutif") {
		t.Errorf("expected visit performance instructions in analyst prompt, got:\n%s", analystPrompt)
	}

	// Test Subagent Tool Metadata
	subTool := NewSubagentTool(pb, nil, nil)
	if subTool.Name() != "delegate_task" {
		t.Errorf("expected tool name 'delegate_task', got '%s'", subTool.Name())
	}
	params := subTool.Parameters()
	if _, ok := params.Properties["instruction"]; !ok {
		t.Errorf("expected parameter 'instruction' in schema")
	}
	if _, ok := params.Properties["role"]; !ok {
		t.Errorf("expected parameter 'role' in schema")
	}
}

func TestFormatUserFriendlyError(t *testing.T) {
	errTimeout := &testCustomError{msg: "Post \"https://api.openai.com/v1\": context deadline exceeded"}
	friendly := FormatUserFriendlyError(errTimeout)
	if !strings.Contains(friendly, "Waktu Tunggu Habis (Timeout)") {
		t.Errorf("expected friendly timeout message, got: %s", friendly)
	}

	errTarget := &testCustomError{msg: "combo 'default' seluruh target gagal: all models down"}
	friendlyTarget := FormatUserFriendlyError(errTarget)
	if !strings.Contains(friendlyTarget, "Layanan AI Sedang Gangguan") {
		t.Errorf("expected friendly provider down message, got: %s", friendlyTarget)
	}

	errCanceled := &testCustomError{msg: "context canceled"}
	friendlyCanceled := FormatUserFriendlyError(errCanceled)
	if !strings.Contains(friendlyCanceled, "Proses Dibatalkan") {
		t.Errorf("expected friendly canceled message, got: %s", friendlyCanceled)
	}
}

type testCustomError struct {
	msg string
}

func (e *testCustomError) Error() string {
	return e.msg
}

func TestMultiChannelMDLoader(t *testing.T) {
	tempDir := t.TempDir()
	loader := NewMDLoader(tempDir)
	promptBld := NewPromptBuilder(loader)

	// 1. Setup Global Files
	globalIdentity := "# Global Assistant\nKamu adalah asisten umum."
	globalSoul := "## SOP Global: Jawab dengan ramah dan ringkas."
	if err := loader.SaveFile("IDENTITY.md", globalIdentity); err != nil {
		t.Fatalf("failed to save global identity: %v", err)
	}
	if err := loader.SaveFile("SOUL.md", globalSoul); err != nil {
		t.Fatalf("failed to save global soul: %v", err)
	}

	// 2. Channel Without Custom MD (Fallback Test)
	promptGeneral, err := promptBld.BuildSystemPrompt(PromptContext{
		ChannelID:   "chan_general",
		ChannelName: "General Channel",
		ChannelType: "telegram",
	})
	if err != nil {
		t.Fatalf("failed to build prompt for general channel: %v", err)
	}
	if !strings.Contains(promptGeneral, "Global Assistant") {
		t.Errorf("expected general channel to inherit global identity, got:\n%s", promptGeneral)
	}
	if !strings.Contains(promptGeneral, "SOP Global") {
		t.Errorf("expected general channel to inherit global soul, got:\n%s", promptGeneral)
	}

	// Check status
	statuses, err := loader.GetChannelMDStatus("chan_general")
	if err != nil {
		t.Fatalf("failed to get channel status: %v", err)
	}
	for _, s := range statuses {
		if s.Filename == "IDENTITY.md" {
			if s.IsCustom || !s.Inherited {
				t.Errorf("expected IDENTITY.md to be inherited for chan_general, got custom=%v, inherited=%v", s.IsCustom, s.Inherited)
			}
		}
	}

	// 3. Override Custom MD for Support Channel
	customSupportIdentity := "# Support Specialist\nKamu adalah spesialis customer support tiket 24/7."
	if err := loader.SaveFileForChannel("chan_support", "IDENTITY.md", customSupportIdentity); err != nil {
		t.Fatalf("failed to save support channel identity: %v", err)
	}

	promptSupport, err := promptBld.BuildSystemPrompt(PromptContext{
		ChannelID:   "chan_support",
		ChannelName: "CS Support Telegram",
		ChannelType: "telegram",
	})
	if err != nil {
		t.Fatalf("failed to build prompt for support channel: %v", err)
	}
	if !strings.Contains(promptSupport, "Support Specialist") {
		t.Errorf("expected support channel to use custom identity, got:\n%s", promptSupport)
	}
	// Verify that SOUL.md is still inherited from global
	if !strings.Contains(promptSupport, "SOP Global") {
		t.Errorf("expected support channel to inherit global soul for untouched files, got:\n%s", promptSupport)
	}

	// Verify general channel is still unaffected
	promptGeneralAfter, _ := promptBld.BuildSystemPrompt(PromptContext{
		ChannelID: "chan_general",
	})
	if !strings.Contains(promptGeneralAfter, "Global Assistant") {
		t.Errorf("general channel should remain on global identity")
	}

	// Check status for chan_support
	supportStatuses, err := loader.GetChannelMDStatus("chan_support")
	if err != nil {
		t.Fatalf("failed to get support status: %v", err)
	}
	for _, s := range supportStatuses {
		if s.Filename == "IDENTITY.md" {
			if !s.IsCustom || s.Inherited {
				t.Errorf("expected IDENTITY.md to be custom for chan_support, got custom=%v, inherited=%v", s.IsCustom, s.Inherited)
			}
		}
	}

	// 4. Test Reset/Delete Channel Override
	if err := loader.DeleteFileForChannel("chan_support", "IDENTITY.md"); err != nil {
		t.Fatalf("failed to delete channel override: %v", err)
	}

	promptSupportAfterReset, err := promptBld.BuildSystemPrompt(PromptContext{
		ChannelID: "chan_support",
	})
	if err != nil {
		t.Fatalf("failed to build prompt after reset: %v", err)
	}
	if !strings.Contains(promptSupportAfterReset, "Global Assistant") {
		t.Errorf("expected support channel to fallback to global after reset, got:\n%s", promptSupportAfterReset)
	}
}

type mockTimeoutProvider struct{}

func (m *mockTimeoutProvider) Name() string                     { return "mock_timeout_prov" }
func (m *mockTimeoutProvider) Type() string                     { return "mock" }
func (m *mockTimeoutProvider) DefaultModel() string             { return "mock-model" }
func (m *mockTimeoutProvider) Models() []string                 { return []string{"mock-model"} }
func (m *mockTimeoutProvider) SetHTTPClient(client interface{}) {}
func (m *mockTimeoutProvider) GenerateChat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, context.DeadlineExceeded
}

func TestProcessMessage_TimeoutAuditLog(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_timeout.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	sm := memory.NewSessionManager(db)
	mm := memory.NewManager(db, config.MemoryConfig{}, nil, nil)
	loader := NewMDLoader(tempDir)
	pb := NewPromptBuilder(loader)
	tr := tools.GetRegistry()

	pm := provider.GetManager()
	mockP := &mockTimeoutProvider{}
	pm.Register(mockP, 1)

	orch := NewOrchestrator(db, sm, mm, pb, tr, pm)

	sess, _ := sm.GetOrCreate("tg_test_chan", "chat_123", "user_456")
	for i := 0; i < 6; i++ {
		_ = sm.AddMessage(sess.ID, "user", fmt.Sprintf("message %d", i), 10)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	userPrompt := "bisa cek log onedrive 50 trakhir?"
	resp, err := orch.ProcessMessage(ctx, UserRequest{
		ChannelType:   "telegram",
		ChannelID:     "tg_test_chan",
		ChatID:        "chat_123",
		UserID:        "user_456",
		UserName:      "Ahmad Nazirul",
		UserPrompt:    userPrompt,
		PreferredProv: "mock_timeout_prov",
	})

	if err == nil {
		t.Fatalf("expected error from timed out ProcessMessage, got nil")
	}
	if resp != nil {
		t.Fatalf("expected nil response on timeout, got %+v", resp)
	}

	// Verify that the timeout error formatting is polite
	friendly := FormatUserFriendlyError(err)
	if !strings.Contains(friendly, "Waktu Tunggu Habis (Timeout)") {
		t.Errorf("expected friendly timeout message, got: %s", friendly)
	}

	// Verify that session messages were truncated to 2 in DB
	cnt, _ := db.CountSessionMessages(sess.ID)
	if cnt > 2 {
		t.Errorf("expected session messages to be truncated to 2 on timeout, got %d", cnt)
	}

	// Verify that audit log was inserted into DB
	logs, err := db.GetRecentAuditLogs(10)
	if err != nil {
		t.Fatalf("failed to get audit logs: %v", err)
	}
	if len(logs) == 0 {
		t.Fatalf("expected at least 1 audit log for timed out request, got 0")
	}

	found := false
	for _, l := range logs {
		if l.ClientRequest == userPrompt {
			found = true
			if l.Status != "timeout" {
				t.Errorf("expected status 'timeout', got '%s'", l.Status)
			}
			if l.UserName != "Ahmad Nazirul" {
				t.Errorf("expected UserName 'Ahmad Nazirul', got '%s'", l.UserName)
			}
			if !strings.Contains(strings.ToLower(l.ErrorMessage), "deadline") {
				t.Errorf("expected error message to mention deadline, got '%s'", l.ErrorMessage)
			}
			break
		}
	}
	if !found {
		t.Errorf("audit log for prompt %q was not found in DB", userPrompt)
	}
}

func TestProcessMessage_CanceledImmediateExit(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_cancel.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	sm := memory.NewSessionManager(db)
	mm := memory.NewManager(db, config.MemoryConfig{}, nil, nil)
	loader := NewMDLoader(tempDir)
	pb := NewPromptBuilder(loader)
	tr := tools.GetRegistry()
	pm := provider.GetManager()

	orch := NewOrchestrator(db, sm, mm, pb, tr, pm)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before execution

	userPrompt := "test stop process"
	resp, err := orch.ProcessMessage(ctx, UserRequest{
		ChannelType: "telegram",
		ChannelID:   "tg_test_chan",
		ChatID:      "chat_cancel",
		UserID:      "user_cancel",
		UserName:    "Cancel User",
		UserPrompt:  userPrompt,
	})

	if err == nil {
		t.Fatalf("expected error from canceled ProcessMessage, got nil")
	}
	if resp != nil {
		t.Fatalf("expected nil response on cancellation, got %+v", resp)
	}

	// Verify audit log status is 'canceled'
	logs, err := db.GetRecentAuditLogs(5)
	if err != nil {
		t.Fatalf("failed to get audit logs: %v", err)
	}
	found := false
	for _, l := range logs {
		if l.ClientRequest == userPrompt {
			found = true
			if l.Status != "canceled" {
				t.Errorf("expected status 'canceled', got '%s'", l.Status)
			}
			break
		}
	}
	if !found {
		t.Errorf("audit log for canceled prompt was not found in DB")
	}
}

type mockVisionProvider struct {
	name     string
	defModel string
}

func (m *mockVisionProvider) Name() string                     { return m.name }
func (m *mockVisionProvider) Type() string                     { return "mock" }
func (m *mockVisionProvider) DefaultModel() string             { return m.defModel }
func (m *mockVisionProvider) Models() []string                 { return []string{m.defModel, "gemini-2.0-flash"} }
func (m *mockVisionProvider) SetHTTPClient(client interface{}) {}
func (m *mockVisionProvider) GenerateChat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{
		Content:      "Berhasil memproses gambar",
		Model:        req.Model,
		ProviderName: m.name,
	}, nil
}

func TestVisionGuard(t *testing.T) {
	tempDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tempDir, "test_vision.db"))
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	provider.InitCatalog(db)

	provMgr := provider.GetManager()
	mockP := &mockVisionProvider{name: "dahl", defModel: "deepseek-v4-flash"}
	provMgr.Register(mockP, 10)

	sm := memory.NewSessionManager(db)
	mm := memory.NewManager(db, config.MemoryConfig{}, nil, nil)
	loader := NewMDLoader(tempDir)
	pb := NewPromptBuilder(loader)
	tr := tools.GetRegistry()

	orch := NewOrchestrator(db, sm, mm, pb, tr, provMgr)

	// 1. Without fallback model: text-only model should reject image with friendly notice
	resp, err := orch.ProcessMessage(context.Background(), UserRequest{
		ChannelType:    "telegram",
		ChannelID:      "chan_1",
		ChatID:         "chat_1",
		UserID:         "user_1",
		UserPrompt:     "Lihat gambar ini",
		AttachedImages: []string{"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="},
		PreferredProv:  "dahl",
		PreferredModel: "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp.Text, "Model Tidak Mendukung Gambar") {
		t.Errorf("expected warning 'Model Tidak Mendukung Gambar', got: %s", resp.Text)
	}

	// 2. With fallback model configured in policy
	policy := db.GetOrCreatePolicy("chat", "chat_1")
	policy.FallbackVisionModel = "dahl:gemini-2.0-flash"
	_ = db.SavePolicy(policy)

	respWithFallback, err := orch.ProcessMessage(context.Background(), UserRequest{
		ChannelType:    "telegram",
		ChannelID:      "chan_1",
		ChatID:         "chat_1",
		UserID:         "user_1",
		UserPrompt:     "Lihat gambar ini",
		AttachedImages: []string{"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="},
		PreferredProv:  "dahl",
		PreferredModel: "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(respWithFallback.Text, "Gambar diproses dengan model vision: gemini-2.0-flash") {
		t.Errorf("expected notice 'Gambar diproses dengan model vision: gemini-2.0-flash', got: %s", respWithFallback.Text)
	}
}

type mockSubagentProv struct {
	name         string
	defaultModel string
	models       []string
	lastReq      provider.ChatRequest
}

func (m *mockSubagentProv) Name() string                     { return m.name }
func (m *mockSubagentProv) Type() string                     { return "openai" }
func (m *mockSubagentProv) DefaultModel() string             { return m.defaultModel }
func (m *mockSubagentProv) Models() []string                 { return m.models }
func (m *mockSubagentProv) SetHTTPClient(client interface{}) {}
func (m *mockSubagentProv) GenerateChat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	m.lastReq = req
	return &provider.ChatResponse{
		Content:     "Hasil sub-agen berhasil",
		Model:       req.Model,
		TotalTokens: 20,
	}, nil
}

func TestSubagentModelInheritanceAndSanitization(t *testing.T) {
	tempDir := t.TempDir()
	loader := NewMDLoader(tempDir)
	pb := NewPromptBuilder(loader)
	tr := tools.GetRegistry()

	mockProv := &mockSubagentProv{
		name:         "test_prov",
		defaultModel: "mock-default-model",
		models:       []string{"mock-default-model", "mock-custom-model"},
	}

	pm := provider.GetManager()
	pm.Register(mockProv, 10)
	defer pm.Unregister(mockProv.Name())

	subTool := NewSubagentTool(pb, tr, pm)

	// Case 1: Active context has parentModel="parent-flash". LLM passes model="auto"
	ctx1 := WithActiveModelInfo(context.Background(), "test_prov", "parent-flash")
	_, err := subTool.Execute(ctx1, map[string]interface{}{
		"role":        "analyst",
		"instruction": "Hitung rasio",
		"model":       "auto",
	})
	if err != nil {
		t.Fatalf("unexpected error in Case 1: %v", err)
	}
	if mockProv.lastReq.Model != "parent-flash" {
		t.Errorf("Case 1: expected inherited model 'parent-flash', got '%s'", mockProv.lastReq.Model)
	}

	// Case 2: Active context has parentModel="parent-flash". LLM hallucinates model="analyst"
	_, err = subTool.Execute(ctx1, map[string]interface{}{
		"role":        "analyst",
		"instruction": "Hitung rasio lagi",
		"model":       "analyst",
	})
	if err != nil {
		t.Fatalf("unexpected error in Case 2: %v", err)
	}
	if mockProv.lastReq.Model != "parent-flash" {
		t.Errorf("Case 2: expected inherited model 'parent-flash' after sanitizing 'analyst', got '%s'", mockProv.lastReq.Model)
	}

	// Case 3: Parent context has empty parentModel="". LLM passes model=""
	// Must fallback to provider's DefaultModel ("mock-default-model"), NOT fictional config model
	ctx3 := WithActiveModelInfo(context.Background(), "test_prov", "")
	_, err = subTool.Execute(ctx3, map[string]interface{}{
		"role":        "coder",
		"instruction": "Tulis fungsi",
		"model":       "",
	})
	if err != nil {
		t.Fatalf("unexpected error in Case 3: %v", err)
	}
	if mockProv.lastReq.Model != "mock-default-model" {
		t.Errorf("Case 3: expected provider DefaultModel 'mock-default-model', got '%s'", mockProv.lastReq.Model)
	}

	// Case 4: Explicit valid provider:model format override
	_, err = subTool.Execute(ctx1, map[string]interface{}{
		"role":        "coder",
		"instruction": "Tulis test",
		"model":       "test_prov:mock-custom-model",
	})
	if err != nil {
		t.Fatalf("unexpected error in Case 4: %v", err)
	}
	if mockProv.lastReq.Model != "mock-custom-model" {
		t.Errorf("Case 4: expected explicit model 'mock-custom-model', got '%s'", mockProv.lastReq.Model)
	}
}

type mockRateLimitRetryProvider struct {
	attempts int
}

func (m *mockRateLimitRetryProvider) Name() string                     { return "mock_ratelimit_prov" }
func (m *mockRateLimitRetryProvider) Type() string                     { return "mock" }
func (m *mockRateLimitRetryProvider) DefaultModel() string             { return "mock-model" }
func (m *mockRateLimitRetryProvider) Models() []string                 { return []string{"mock-model"} }
func (m *mockRateLimitRetryProvider) SetHTTPClient(client interface{}) {}
func (m *mockRateLimitRetryProvider) GenerateChat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	m.attempts++
	if m.attempts <= 2 {
		return nil, fmt.Errorf("status 429: rate limit reached (RPM). Please try again in 50ms")
	}
	return &provider.ChatResponse{
		Content: "Resilient retry succeeded!",
	}, nil
}

func TestProcessMessage_ResilientRateLimitRetry(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_ratelimit.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	sm := memory.NewSessionManager(db)
	mm := memory.NewManager(db, config.MemoryConfig{}, nil, nil)
	loader := NewMDLoader(tempDir)
	pb := NewPromptBuilder(loader)
	tr := tools.GetRegistry()

	pm := provider.NewManager()
	mockP := &mockRateLimitRetryProvider{}
	pm.Register(mockP, 1)

	orch := NewOrchestrator(db, sm, mm, pb, tr, pm)

	sess, _ := sm.GetOrCreate("tg_test_chan", "chat_rl_123", "user_rl_456")
	_ = sm.AddMessage(sess.ID, "user", "previous message 1", 10)
	_ = sm.AddMessage(sess.ID, "assistant", "previous reply 1", 10)
	_ = sm.AddMessage(sess.ID, "user", "previous message 2", 10)

	var progressUpdates []string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := orch.ProcessMessage(ctx, UserRequest{
		ChannelType:   "telegram",
		ChannelID:     "tg_test_chan",
		ChatID:        "chat_rl_123",
		UserID:        "user_rl_456",
		UserName:      "TestUser",
		UserPrompt:    "Halo, tolong bantu saya",
		PreferredProv: "mock_ratelimit_prov",
		OnProgress: func(status string) {
			progressUpdates = append(progressUpdates, status)
		},
	})

	if err != nil {
		t.Fatalf("expected success after resilient retries, got error: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Text, "Resilient retry succeeded!") {
		t.Fatalf("expected successful response, got %+v", resp)
	}

	// Verify that provider was called at least 3 times
	if mockP.attempts != 3 {
		t.Errorf("expected 3 provider attempts, got %d", mockP.attempts)
	}

	// Verify that history was preserved (not truncated by mistake on rate limit)
	historyMessages, err := sm.GetHistory(sess.ID, 10)
	if err != nil {
		t.Fatalf("failed to get session messages: %v", err)
	}
	// Original 3 messages + current user message + assistant reply = 5 messages
	if len(historyMessages) < 4 {
		t.Errorf("expected session history to be preserved (> 3 messages), got %d", len(historyMessages))
	}

	// Verify that progress updates were received
	if len(progressUpdates) == 0 {
		t.Errorf("expected progress updates during rate limit wait, got none")
	}
}

type mockContextExceededProvider struct {
	attempts int
}

func (m *mockContextExceededProvider) Name() string                     { return "mock_context_prov" }
func (m *mockContextExceededProvider) Type() string                     { return "mock" }
func (m *mockContextExceededProvider) DefaultModel() string             { return "mock-model" }
func (m *mockContextExceededProvider) Models() []string                 { return []string{"mock-model"} }
func (m *mockContextExceededProvider) SetHTTPClient(client interface{}) {}
func (m *mockContextExceededProvider) GenerateChat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	m.attempts++
	if m.attempts == 1 {
		return nil, fmt.Errorf("context_length_exceeded: maximum context length is 4096 tokens")
	}
	return &provider.ChatResponse{
		Content: "Compacted retry succeeded!",
	}, nil
}

func TestProcessMessage_ContextLengthExceededCompaction(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_context.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	sm := memory.NewSessionManager(db)
	mm := memory.NewManager(db, config.MemoryConfig{}, nil, nil)
	loader := NewMDLoader(tempDir)
	pb := NewPromptBuilder(loader)
	tr := tools.GetRegistry()

	pm := provider.NewManager()
	mockP := &mockContextExceededProvider{}
	pm.Register(mockP, 1)

	orch := NewOrchestrator(db, sm, mm, pb, tr, pm)

	sess, _ := sm.GetOrCreate("tg_test_chan", "chat_ctx_123", "user_ctx_456")
	for i := 0; i < 6; i++ {
		_ = sm.AddMessage(sess.ID, "user", fmt.Sprintf("long history message %d", i), 100)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := orch.ProcessMessage(ctx, UserRequest{
		ChannelType:   "telegram",
		ChannelID:     "tg_test_chan",
		ChatID:        "chat_ctx_123",
		UserID:        "user_ctx_456",
		UserName:      "TestUser",
		UserPrompt:    "Pertanyaan berikutnya",
		PreferredProv: "mock_context_prov",
	})

	if err != nil {
		t.Fatalf("expected success after context compaction retry, got error: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Text, "Compacted retry succeeded!") {
		t.Fatalf("expected successful response, got %+v", resp)
	}
	if mockP.attempts != 2 {
		t.Errorf("expected 2 provider attempts (1 failed context + 1 compacted success), got %d", mockP.attempts)
	}
}

type mockRescueToolProvider struct {
	attempts int
}

func (m *mockRescueToolProvider) Name() string                     { return "mock_rescue_prov" }
func (m *mockRescueToolProvider) Type() string                     { return "mock" }
func (m *mockRescueToolProvider) DefaultModel() string             { return "mock-model" }
func (m *mockRescueToolProvider) Models() []string                 { return []string{"mock-model"} }
func (m *mockRescueToolProvider) SetHTTPClient(client interface{}) {}
func (m *mockRescueToolProvider) GenerateChat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	m.attempts++
	if m.attempts == 1 {
		return &provider.ChatResponse{
			ToolCalls: []provider.ToolCall{
				{
					ID:        "call_dt_1",
					Name:      "get_current_datetime",
					Arguments: map[string]interface{}{},
				},
			},
		}, nil
	}
	return nil, context.DeadlineExceeded
}

func TestProcessMessage_DeadlineGuardToolRescue(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_rescue.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	sm := memory.NewSessionManager(db)
	mm := memory.NewManager(db, config.MemoryConfig{}, nil, nil)
	loader := NewMDLoader(tempDir)
	pb := NewPromptBuilder(loader)
	tr := tools.GetRegistry()

	pm := provider.NewManager()
	mockP := &mockRescueToolProvider{}
	pm.Register(mockP, 1)

	orch := NewOrchestrator(db, sm, mm, pb, tr, pm)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := orch.ProcessMessage(ctx, UserRequest{
		ChannelType:   "telegram",
		ChannelID:     "tg_test_chan",
		ChatID:        "chat_rescue_123",
		UserID:        "user_rescue_456",
		UserName:      "TestUser",
		UserPrompt:    "Jam berapa sekarang?",
		PreferredProv: "mock_rescue_prov",
	})

	if err != nil {
		t.Fatalf("expected graceful partial response instead of error, got: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected non-nil rescued response")
	}
	if !strings.Contains(resp.Text, "Hasil Data Parsial") {
		t.Errorf("expected rescue header 'Hasil Data Parsial', got: %s", resp.Text)
	}
}
