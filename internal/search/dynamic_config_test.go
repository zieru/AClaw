package search

import (
	"os"
	"path/filepath"
	"testing"

	"goassistant/internal/config"
	"goassistant/internal/storage"
)

func TestDynamicConfig_SaveAndLoad(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "search_dyn_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer db.Close()

	fallback := config.SearchConfig{
		Enabled:         true,
		Provider:        "auto",
		Strategy:        "fallback",
		MaxResults:      5,
		FallbackEnabled: true,
		TimeoutSeconds:  15,
		Tavily: config.TavilyConfig{
			APIKey:        "tvly-initial",
			BaseURL:       "https://api.tavily.com",
			SearchDepth:   "basic",
			IncludeAnswer: true,
		},
		Firecrawl: config.FirecrawlConfig{
			APIKey:  "fc-initial",
			BaseURL: "https://api.firecrawl.dev",
		},
	}

	// 1. First load with empty DB returns fallback
	loaded1 := LoadDynamicConfig(db, fallback)
	if loaded1.Tavily.APIKey != "tvly-initial" || loaded1.Provider != "auto" {
		t.Fatalf("expected fallback values, got %+v", loaded1)
	}

	// 2. Modify and Save
	updated := loaded1
	updated.Provider = "firecrawl"
	updated.Strategy = "roundrobin"
	updated.MaxResults = 10
	updated.Tavily.APIKey = "tvly-updated"
	updated.Tavily.SearchDepth = "advanced"
	updated.Tavily.IncludeAnswer = false
	updated.Firecrawl.APIKey = "fc-updated"
	updated.Firecrawl.BaseURL = "http://localhost:3002"

	if err := SaveDynamicConfig(db, updated); err != nil {
		t.Fatalf("failed to save dynamic search config: %v", err)
	}

	// 3. Load again and verify persistence
	loaded2 := LoadDynamicConfig(db, fallback)
	if loaded2.Provider != "firecrawl" {
		t.Errorf("expected provider 'firecrawl', got '%s'", loaded2.Provider)
	}
	if loaded2.Strategy != "roundrobin" {
		t.Errorf("expected strategy 'roundrobin', got '%s'", loaded2.Strategy)
	}
	if loaded2.MaxResults != 10 {
		t.Errorf("expected max_results 10, got %d", loaded2.MaxResults)
	}
	if loaded2.Tavily.APIKey != "tvly-updated" {
		t.Errorf("expected tavily key 'tvly-updated', got '%s'", loaded2.Tavily.APIKey)
	}
	if loaded2.Tavily.SearchDepth != "advanced" {
		t.Errorf("expected tavily search depth 'advanced', got '%s'", loaded2.Tavily.SearchDepth)
	}
	if loaded2.Tavily.IncludeAnswer != false {
		t.Errorf("expected tavily include answer false, got true")
	}
	if loaded2.Firecrawl.APIKey != "fc-updated" {
		t.Errorf("expected firecrawl key 'fc-updated', got '%s'", loaded2.Firecrawl.APIKey)
	}
	if loaded2.Firecrawl.BaseURL != "http://localhost:3002" {
		t.Errorf("expected firecrawl baseURL 'http://localhost:3002', got '%s'", loaded2.Firecrawl.BaseURL)
	}
}
