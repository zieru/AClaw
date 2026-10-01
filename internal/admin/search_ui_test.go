package admin

import (
	"strings"
	"testing"

	"goassistant/internal/config"
	"goassistant/internal/search"
)

func TestSearchUIHandler_BuildKeyboard(t *testing.T) {
	cfg := &config.AppConfig{}
	cfg.Search.Enabled = true
	cfg.Search.Provider = "auto"
	cfg.Search.Strategy = "fallback"
	cfg.Search.FallbackEnabled = true

	ui := NewSearchUIHandler(cfg)

	// 1. Fallback mode keyboard
	kb := ui.BuildKeyboard(cfg.Search)
	if kb == nil || len(kb.InlineKeyboard) == 0 {
		t.Fatal("expected non-empty inline keyboard")
	}

	foundRoundRobinBtn := false
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if strings.Contains(btn.Text, "Round-Robin") {
				foundRoundRobinBtn = true
			}
		}
	}
	if !foundRoundRobinBtn {
		t.Error("expected button to switch to Round-Robin")
	}

	// 2. Round-Robin mode keyboard
	cfg.Search.Strategy = "roundrobin"
	cfg.Search.Provider = "tavily"
	kb2 := ui.BuildKeyboard(cfg.Search)
	foundFallbackBtn := false
	foundTavilyActive := false
	for _, row := range kb2.InlineKeyboard {
		for _, btn := range row {
			if strings.Contains(btn.Text, "Fallback") {
				foundFallbackBtn = true
			}
			if strings.Contains(btn.Text, "🔘 Tavily") {
				foundTavilyActive = true
			}
		}
	}
	if !foundFallbackBtn {
		t.Error("expected button to switch to Fallback")
	}
	if !foundTavilyActive {
		t.Error("expected active marker on Tavily button")
	}
}

func TestSearchUIHandler_MaskAPIKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "***"},
		{"short", "***"},
		{"tvly-123456789", "tvly***6789"},
		{"fc-secretkey1234", "fc-s***1234"},
	}

	for _, tc := range tests {
		got := maskAPIKey(tc.input)
		if got != tc.expected {
			t.Errorf("maskAPIKey(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestSearchUIHandler_DynamicKeyUpdates(t *testing.T) {
	eng := search.InitGlobalEngine(config.SearchConfig{
		Enabled:  true,
		Provider: "auto",
		Strategy: "fallback",
	})

	appCfg := &config.AppConfig{}
	appCfg.Search.Enabled = true
	ui := NewSearchUIHandler(appCfg)

	// Set Tavily Key
	ui.waitingTavilyKey.Store(int64(12345), true)
	eng.SetTavilyKey("tvly-test-dynamic-123")
	if eng.Config().Tavily.APIKey != "tvly-test-dynamic-123" {
		t.Errorf("unexpected tavily key: %s", eng.Config().Tavily.APIKey)
	}

	// Clear Tavily Key
	eng.SetTavilyKey("")
	if eng.Config().Tavily.APIKey != "" {
		t.Errorf("expected empty tavily key, got: %s", eng.Config().Tavily.APIKey)
	}
}
