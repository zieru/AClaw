package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

type mockPasswordPrompter struct {
	promptCalled bool
	lastTitle    string
	lastDesc     string
	returnPass   string
	finishCalled bool
}

func (m *mockPasswordPrompter) PromptPassword(ctx context.Context, title, description string) (string, func(), error) {
	m.promptCalled = true
	m.lastTitle = title
	m.lastDesc = description
	finish := func() {
		m.finishCalled = true
	}
	return m.returnPass, finish, nil
}

func TestBashToolWithPasswordPrompter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping sudo bash test on Windows host")
	}

	sessionKey := "test_chat_bash_prompt"
	ClearSudoSession(sessionKey)

	mockPrompter := &mockPasswordPrompter{
		returnPass: "mySudoPassword123",
	}

	ctx := context.Background()
	ctx = context.WithValue(ctx, "chat_id", sessionKey)
	ctx = WithPasswordPrompter(ctx, mockPrompter)

	tool := &BashTool{}
	args := map[string]interface{}{
		"command": "sudo echo 'testing root'",
	}

	// Execute tool
	_, _ = tool.Execute(ctx, args)

	if !mockPrompter.promptCalled {
		t.Fatalf("expected PasswordPrompter to be called for sudo command")
	}
	if !mockPrompter.finishCalled {
		t.Fatalf("expected finish() cleanup callback to be called")
	}
	if !strings.Contains(mockPrompter.lastDesc, "sudo echo") {
		t.Errorf("expected lastDesc to contain command, got: %s", mockPrompter.lastDesc)
	}

	// Verify password was cached in SudoSession
	cached := GetSudoSession(sessionKey)
	if cached != "mySudoPassword123" {
		t.Errorf("expected cached password to be 'mySudoPassword123', got: %q", cached)
	}

	// Run second sudo command with active session - should NOT prompt again!
	mockPrompter.promptCalled = false
	mockPrompter.finishCalled = false

	args2 := map[string]interface{}{
		"command": "sudo echo 'second root command'",
	}
	_, _ = tool.Execute(ctx, args2)

	if mockPrompter.promptCalled {
		t.Errorf("expected prompter NOT to be called when valid sudo session exists")
	}

	ClearSudoSession(sessionKey)
}

func TestBashToolContextHelpers(t *testing.T) {
	ctx := context.Background()
	if GetPasswordPrompter(ctx) != nil {
		t.Errorf("expected nil prompter from empty context")
	}

	mock := &mockPasswordPrompter{returnPass: "test"}
	ctx = WithPasswordPrompter(ctx, mock)
	got := GetPasswordPrompter(ctx)
	if got == nil {
		t.Fatalf("expected non-nil prompter")
	}

	pass, finish, err := got.PromptPassword(ctx, "title", "desc")
	if err != nil || pass != "test" || finish == nil {
		t.Errorf("unexpected PromptPassword result: pass=%q, err=%v", pass, err)
	}
	finish()
	if !mock.finishCalled {
		t.Errorf("expected finish to be called")
	}
}
