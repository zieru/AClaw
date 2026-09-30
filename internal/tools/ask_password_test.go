package tools

import (
	"context"
	"strings"
	"testing"
)

func TestAskPasswordToolMetadata(t *testing.T) {
	tool := NewAskPasswordTool()
	if tool.Name() != "ask_password" {
		t.Errorf("tool.Name() = %q, want 'ask_password'", tool.Name())
	}
	params := tool.Parameters()
	if _, ok := params.Properties["title"]; !ok {
		t.Errorf("expected 'title' parameter")
	}
	if _, ok := params.Properties["description"]; !ok {
		t.Errorf("expected 'description' parameter")
	}
	if _, ok := params.Properties["target"]; !ok {
		t.Errorf("expected 'target' parameter")
	}
}

func TestAskPasswordToolExecutionWithoutPrompter(t *testing.T) {
	tool := NewAskPasswordTool()
	ctx := context.Background()

	_, err := tool.Execute(ctx, map[string]interface{}{
		"title":       "Database Password",
		"description": "Perlu koneksi DB",
	})
	if err == nil {
		t.Fatalf("expected error when prompter is missing from context")
	}
}

func TestAskPasswordToolExecutionSuccess(t *testing.T) {
	tool := NewAskPasswordTool()
	sessionKey := "test_chat_ask_pass"
	ClearSudoSession(sessionKey)

	mockPrompter := &mockPasswordPrompter{
		returnPass: "postgresSecretPass!@#",
	}

	ctx := context.Background()
	ctx = context.WithValue(ctx, "chat_id", sessionKey)
	ctx = WithPasswordPrompter(ctx, mockPrompter)

	// Test general password request
	out, err := tool.Execute(ctx, map[string]interface{}{
		"title":       "Password PostgreSQL",
		"description": "Migrasi database",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mockPrompter.promptCalled {
		t.Fatalf("expected prompter to be called")
	}
	if !mockPrompter.finishCalled {
		t.Fatalf("expected finish to be called")
	}

	// CRITICAL: verify the secret password is NOT leaked in output returned to AI
	if strings.Contains(out, "postgresSecretPass!@#") {
		t.Fatalf("CRITICAL SECURITY FLAW: password was leaked in tool output: %s", out)
	}
	if !strings.Contains(out, "[SUCCESS]") {
		t.Errorf("expected '[SUCCESS]' in tool output, got: %s", out)
	}

	// Test target='sudo' option
	mockPrompter.returnPass = "myNewSudoPass999"
	mockPrompter.promptCalled = false
	mockPrompter.finishCalled = false

	outSudo, err := tool.Execute(ctx, map[string]interface{}{
		"title":       "Password Sudo Server",
		"description": "Instalasi Docker",
		"target":      "sudo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(outSudo, "myNewSudoPass999") {
		t.Fatalf("CRITICAL: sudo password was leaked in tool output: %s", outSudo)
	}

	// Verify it was stored in SudoSession
	cached := GetSudoSession(sessionKey)
	if cached != "myNewSudoPass999" {
		t.Errorf("expected SudoSession to store 'myNewSudoPass999', got: %q", cached)
	}

	ClearSudoSession(sessionKey)
}
