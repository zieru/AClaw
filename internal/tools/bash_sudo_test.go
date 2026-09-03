package tools

import (
	"context"
	"testing"
)

func TestContainsSudo(t *testing.T) {
	tests := []struct {
		cmd      string
		expected bool
	}{
		{"sudo apt update", true},
		{"apt update && sudo systemctl restart nginx", true},
		{"echo hello | sudo tee /tmp/test.txt", true},
		{"sudo -n whoami", true},
		{"ls -la", false},
		{"python3 script.py", false},
		{"grep sudoers /etc/passwd", false},
		{"echo 'sudo in text' ", true}, // contains sudo token
	}

	for _, tt := range tests {
		got := containsSudo(tt.cmd)
		if got != tt.expected {
			t.Errorf("containsSudo(%q) = %v, expected %v", tt.cmd, got, tt.expected)
		}
	}
}

func TestInjectSudoStdinFlag(t *testing.T) {
	cmd := "sudo apt update"
	injected := injectSudoStdinFlag(cmd)
	expected := "sudo -S -p '' apt update"
	if injected != expected {
		t.Errorf("expected %q, got %q", expected, injected)
	}

	// Already has sudo -S
	if injectSudoStdinFlag("sudo -S apt update") != "sudo -S apt update" {
		t.Errorf("should not duplicate flag")
	}
}

func TestInjectSudoNonInteractive(t *testing.T) {
	cmd := "sudo apt update"
	injected := injectSudoNonInteractive(cmd)
	expected := "sudo -n apt update"
	if injected != expected {
		t.Errorf("expected %q, got %q", expected, injected)
	}
}

func TestSudoSessionTTL(t *testing.T) {
	key := "chat_12345"
	pass := "secret123"

	SetSudoSession(key, pass)
	got := GetSudoSession(key)
	if got != pass {
		t.Fatalf("expected %q, got %q", pass, got)
	}

	ClearSudoSession(key)
	gotAfterClear := GetSudoSession(key)
	if gotAfterClear != "" {
		t.Fatalf("expected empty after clear, got %q", gotAfterClear)
	}
}

func TestBashToolParameters(t *testing.T) {
	tool := &BashTool{}
	params := tool.Parameters()
	if _, ok := params.Properties["sudo_password"]; !ok {
		t.Fatalf("expected 'sudo_password' in parameters schema")
	}
}

func TestBashToolExecuteSimple(t *testing.T) {
	tool := &BashTool{}
	ctx := context.Background()

	out, err := tool.Execute(ctx, map[string]interface{}{
		"command": "echo hello_goassistant",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatalf("expected non-empty output")
	}
}
