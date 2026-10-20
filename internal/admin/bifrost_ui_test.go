package admin

import (
	"path/filepath"
	"strings"
	"testing"

	bf "goassistant/internal/bifrost"
	"goassistant/internal/storage"
)

func TestBifrostUI_DashboardAndSubmenus(t *testing.T) {
	tempDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tempDir, "test_bifrost_ui.db"))
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	ui := NewBifrostUIHandler(db, nil)

	// Test dashboard rendering
	text, markup := ui.RenderBifrostDashboard()
	if !strings.Contains(text, "BIFROST AI GATEWAY SETTINGS") {
		t.Errorf("expected dashboard title, got:\n%s", text)
	}
	if markup == nil || len(markup.InlineKeyboard) == 0 {
		t.Errorf("expected dashboard buttons")
	}

	// Test submenus
	wText, wMarkup := ui.RenderWorkersMenu()
	if !strings.Contains(wText, "CONCURRENCY & WORKERS") || wMarkup == nil {
		t.Errorf("workers menu failed")
	}

	nText, nMarkup := ui.RenderNetworkMenu()
	if !strings.Contains(nText, "NETWORK & TIMEOUT") || nMarkup == nil {
		t.Errorf("network menu failed")
	}

	sText, sMarkup := ui.RenderStreamMenu()
	if !strings.Contains(sText, "STREAM RESILIENCE") || sMarkup == nil {
		t.Errorf("stream menu failed")
	}

	cText, cMarkup := ui.RenderCacheMenu()
	if !strings.Contains(cText, "PROMPT CACHING") || cMarkup == nil {
		t.Errorf("cache menu failed")
	}

	pText, pMarkup := ui.RenderProxyMenu()
	if !strings.Contains(pText, "UPSTREAM PROXY GATEWAY") || pMarkup == nil {
		t.Errorf("proxy menu failed")
	}
}

func TestBifrostDynamicConfigSaveAndLoad(t *testing.T) {
	tempDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tempDir, "test_bifrost_dyn.db"))
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	cfg := bf.LoadDynamicConfig(db)
	if cfg.Concurrency != 8 {
		t.Errorf("expected default concurrency 8, got %d", cfg.Concurrency)
	}

	cfg.Concurrency = 16
	cfg.TimeoutSeconds = 120
	cfg.MaxRetries = 3
	cfg.InsecureSkipVerify = true
	cfg.ProxyURL = "http://127.0.0.1:8080"
	if err := bf.SaveDynamicConfig(db, cfg); err != nil {
		t.Fatalf("failed to save dynamic config: %v", err)
	}

	loaded := bf.LoadDynamicConfig(db)
	if loaded.Concurrency != 16 {
		t.Errorf("expected concurrency 16, got %d", loaded.Concurrency)
	}
	if loaded.TimeoutSeconds != 120 {
		t.Errorf("expected timeout 120, got %d", loaded.TimeoutSeconds)
	}
	if loaded.MaxRetries != 3 {
		t.Errorf("expected retries 3, got %d", loaded.MaxRetries)
	}
	if !loaded.InsecureSkipVerify {
		t.Errorf("expected InsecureSkipVerify true")
	}
	if loaded.ProxyURL != "http://127.0.0.1:8080" {
		t.Errorf("expected proxy http://127.0.0.1:8080, got %s", loaded.ProxyURL)
	}
}
