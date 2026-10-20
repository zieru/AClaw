package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseGoogleAuthInput(t *testing.T) {
	// Test Case 1: Standard Cookie string
	cookieStr := "__Secure-1PSID=g.a000abc123; __Secure-1PSIDTS=sidts_val; SID=sid_val"
	cookies, err := ParseGoogleAuthInput(cookieStr)
	if err != nil {
		t.Fatalf("unexpected error parsing cookie string: %v", err)
	}
	if cookies["__Secure-1PSID"] != "g.a000abc123" {
		t.Errorf("expected __Secure-1PSID=g.a000abc123, got %s", cookies["__Secure-1PSID"])
	}
	if cookies["__Secure-1PSIDTS"] != "sidts_val" {
		t.Errorf("expected __Secure-1PSIDTS=sidts_val, got %s", cookies["__Secure-1PSIDTS"])
	}

	// Test Case 2: JSON cookie list
	jsonCookies := `[{"name":"__Secure-1PSID","value":"json_psid_val"},{"name":"__Secure-1PSIDTS","value":"json_psidts_val"}]`
	cookies2, err := ParseGoogleAuthInput(jsonCookies)
	if err != nil {
		t.Fatalf("unexpected error parsing JSON cookies: %v", err)
	}
	if cookies2["__Secure-1PSID"] != "json_psid_val" {
		t.Errorf("expected __Secure-1PSID=json_psid_val, got %s", cookies2["__Secure-1PSID"])
	}
}

func TestIsGeminiWeb(t *testing.T) {
	testCases := []struct {
		input    string
		expected bool
	}{
		{"gemini_web", true},
		{"gemini-web-pro", true},
		{"Gemini Web (Google Auth)", true},
		{"gemini_scrape", true},
		{"gemini_web:gemini-3.5-flash-thinking", true},
		{"gemini-web-ultra", true},
		{"google gemini", false},
		{"openai", false},
		{"deepseek", false},
	}
	for _, tc := range testCases {
		if got := IsGeminiWeb(tc.input); got != tc.expected {
			t.Errorf("IsGeminiWeb(%q) = %v; want %v", tc.input, got, tc.expected)
		}
	}
}

func TestGeminiWebProviderInterface(t *testing.T) {
	p := NewGeminiWebProvider("gemini_web", "__Secure-1PSID=test_psid", "gemini-3.5-flash-thinking", nil)
	if p.Name() != "gemini_web" {
		t.Errorf("expected name gemini_web, got %s", p.Name())
	}
	if p.Type() != "gemini_web" {
		t.Errorf("expected type gemini_web, got %s", p.Type())
	}
	if p.DefaultModel() != "gemini-3.5-flash-thinking" {
		t.Errorf("expected default model gemini-3.5-flash-thinking, got %s", p.DefaultModel())
	}
	if !IsFreeProvider(p.Type()) {
		t.Errorf("expected IsFreeProvider(gemini_web) to be true")
	}
	if len(p.Models()) == 0 {
		t.Errorf("expected models list to be non-empty")
	}
}

func TestGeminiWebRunnerDiagnostic(t *testing.T) {
	p := NewGeminiWebProvider("gemini_web", "", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := p.RunDiagnostic(ctx)
	if err != nil {
		t.Fatalf("RunDiagnostic failed: %v", err)
	}
	if res["status"] != "ok" {
		t.Errorf("expected status ok, got %v", res["status"])
	}
	if res["import_success"] != true {
		t.Errorf("expected import_success true, got %v", res["import_success"])
	}
}

func TestReadRemainingEvents(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gemini_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "resp.jsonl")
	ev1 := IPCEvent{Type: "chunk", Delta: "Halo "}
	ev2 := IPCEvent{Type: "chunk", Delta: "dunia!"}
	ev3 := IPCEvent{Type: "done", Content: "Halo dunia!", TotalTokens: 4}

	b1, _ := json.Marshal(ev1)
	b2, _ := json.Marshal(ev2)
	b3, _ := json.Marshal(ev3)

	content := string(b1) + "\n" + string(b2) + "\n" + string(b3) + "\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	var offset int64
	var collected []IPCEvent
	readRemainingEvents(filePath, &offset, func(ev IPCEvent) {
		collected = append(collected, ev)
	})

	if len(collected) != 3 {
		t.Fatalf("expected 3 events, got %d", len(collected))
	}
	if collected[0].Delta != "Halo " {
		t.Errorf("unexpected first delta: %s", collected[0].Delta)
	}
	if collected[1].Delta != "dunia!" {
		t.Errorf("unexpected second delta: %s", collected[1].Delta)
	}
	if collected[2].Type != "done" || collected[2].Content != "Halo dunia!" {
		t.Errorf("unexpected final event: %+v", collected[2])
	}
}
