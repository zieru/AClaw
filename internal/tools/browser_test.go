package tools

import (
	"strings"
	"testing"
)

func TestBrowserAutomationTool_Schema(t *testing.T) {
	tool := &BrowserAutomationTool{}

	if tool.Name() != "browser" {
		t.Fatalf("expected tool name 'browser', got '%s'", tool.Name())
	}

	params := tool.Parameters()
	if params.Type != "object" {
		t.Fatalf("expected object schema, got '%s'", params.Type)
	}

	// Verify required actions and parameters
	requiredKeys := []string{"action", "index", "selector", "text", "clear", "press_enter", "key", "direction", "som", "url"}
	for _, key := range requiredKeys {
		if _, exists := params.Properties[key]; !exists {
			t.Errorf("expected property '%s' in schema properties", key)
		}
	}

	actionProp := params.Properties["action"]
	expectedActions := []string{"open", "click", "type", "press_key", "scroll", "screenshot", "eval_js"}
	for _, act := range expectedActions {
		found := false
		for _, enumVal := range actionProp.Enum {
			if enumVal == act {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("action '%s' not found in schema action enum", act)
		}
	}
}

func TestBrowserAutomationTool_ExtractCleanText(t *testing.T) {
	html := `
		<!DOCTYPE html>
		<html>
		<head>
			<title>Test Web</title>
			<script>var x = 10;</script>
			<style>body { color: red; }</style>
		</head>
		<body>
			<h1>Selamat Datang</h1>
			<p>Ini adalah halaman tes untuk browser automation.</p>
			<div>
				<span>Konten dalam div.</span>
			</div>
		</body>
		</html>
	`

	cleaned := extractCleanText(html)
	if strings.Contains(cleaned, "var x = 10") {
		t.Errorf("expected script content to be removed")
	}
	if strings.Contains(cleaned, "body { color: red; }") {
		t.Errorf("expected style content to be removed")
	}
	if !strings.Contains(cleaned, "Selamat Datang") {
		t.Errorf("expected header text 'Selamat Datang' to be preserved")
	}
	if !strings.Contains(cleaned, "Konten dalam div.") {
		t.Errorf("expected body text to be preserved")
	}
}
