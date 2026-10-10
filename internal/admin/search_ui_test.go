package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goassistant/internal/config"
	"goassistant/internal/search"
	"goassistant/internal/storage"
)

func TestSearchUIHandler_BuildKeyboard(t *testing.T) {
	cfg := &config.AppConfig{}
	cfg.Search.Enabled = true
	cfg.Search.Provider = "auto"
	cfg.Search.Strategy = "fallback"
	cfg.Search.FallbackEnabled = true

	ui := NewSearchUIHandler(cfg, nil)

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
	ui := NewSearchUIHandler(appCfg, nil)

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

func TestSearchUIHandler_PersistenceWithDB(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "search_ui_db_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := storage.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer db.Close()

	eng := search.InitGlobalEngine(config.SearchConfig{
		Enabled:    true,
		Provider:   "auto",
		Strategy:   "fallback",
		MaxResults: 5,
		Tavily: config.TavilyConfig{
			BaseURL:     "https://api.tavily.com",
			SearchDepth: "basic",
		},
		Firecrawl: config.FirecrawlConfig{
			BaseURL: "https://api.firecrawl.dev",
		},
	})

	appCfg := &config.AppConfig{}
	appCfg.Search = eng.Config()
	ui := NewSearchUIHandler(appCfg, db)

	// Simulate engine update via ui
	eng.SetTavilyKey("tvly-persisted-xyz")
	eng.SetFirecrawlKey("fc-persisted-abc")
	eng.SetTavilyDepth("advanced")
	eng.SetFirecrawlBaseURL("http://localhost:3002")
	eng.SetMaxResults(10)
	ui.persistConfig()

	// Verify database record
	savedJSON, err := db.GetSetting("search_config", "")
	if err != nil {
		t.Fatalf("failed to get search_config: %v", err)
	}
	if !strings.Contains(savedJSON, "tvly-persisted-xyz") {
		t.Errorf("saved JSON missing tavily key: %s", savedJSON)
	}
	if !strings.Contains(savedJSON, "fc-persisted-abc") {
		t.Errorf("saved JSON missing firecrawl key: %s", savedJSON)
	}
	if !strings.Contains(savedJSON, "http://localhost:3002") {
		t.Errorf("saved JSON missing firecrawl baseURL: %s", savedJSON)
	}
}
