package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if !strings.Contains(fullFooter, "🤖 gpt-4o-mini") {
		t.Errorf("expected model name in full footer, got '%s'", fullFooter)
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


