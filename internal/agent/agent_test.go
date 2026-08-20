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
	offFooter := FormatFooter("off", 100, 50, 150, 500*1000*1000, "gpt-4o", "openai", nil)
	if offFooter != "" {
		t.Fatalf("expected empty footer for mode 'off', got '%s'", offFooter)
	}

	// Mode "tokens"
	tokensFooter := FormatFooter("tokens", 1000, 250, 1250, 500*1000*1000, "gpt-4o", "openai", nil)
	if !strings.Contains(tokensFooter, "🪙 1,250 tokens") {
		t.Fatalf("expected '🪙 1,250 tokens', got '%s'", tokensFooter)
	}

	// Mode "full"
	fullFooter := FormatFooter("full", 1120, 300, 1420, 850*1000*1000, "gpt-4o-mini", "9router", []string{"search_web", "read_db"})
	if !strings.Contains(fullFooter, "⚡ 850ms") {
		t.Errorf("expected latency '⚡ 850ms' in full footer, got '%s'", fullFooter)
	}
	if !strings.Contains(fullFooter, "🪙 1,420 (in: 1,120 / out: 300)") {
		t.Errorf("expected token breakdown in full footer, got '%s'", fullFooter)
	}
	if !strings.Contains(fullFooter, "🔧 2 tools") {
		t.Errorf("expected tools count in full footer, got '%s'", fullFooter)
	}
	if !strings.Contains(fullFooter, "🤖 gpt-4o-mini") {
		t.Errorf("expected model name in full footer, got '%s'", fullFooter)
	}
}
