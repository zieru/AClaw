package admin

import (
	"fmt"
	"path/filepath"
	"testing"

	"goassistant/internal/provider"
	"goassistant/internal/storage"
)

func TestProviderUIWithHundredsOfModels(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_provider_ui.db")

	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	pm := provider.GetManager()
	ui := NewProviderUI(db, pm, nil)

	// Create 500 models
	var largeModels []string
	for i := 1; i <= 500; i++ {
		largeModels = append(largeModels, fmt.Sprintf("custom-provider-model-v%d-chat-ultra-long-name", i))
	}

	record := &storage.ProviderRecord{
		ID:           "test_large_prov",
		Name:         "LargeProvider",
		Type:         "openai",
		DefaultModel: largeModels[0],
		Models:       largeModels,
		IsActive:     true,
		KeyStrategy:  "round-robin",
	}

	if err := db.SaveProvider(record); err != nil {
		t.Fatalf("failed to save provider: %v", err)
	}

	// 1. Verify RenderProvidersList length is well within Telegram limit (4096 chars)
	listOutput := ui.RenderProvidersList()
	if len(listOutput) > 4000 {
		t.Errorf("RenderProvidersList output too long: got %d characters, want <= 4000", len(listOutput))
	}

	// 2. Verify RenderProviderDashboard length is well within Telegram limit (4096 chars)
	dashOutput, kb := ui.RenderProviderDashboard(record)
	if len(dashOutput) > 4000 {
		t.Errorf("RenderProviderDashboard output too long: got %d characters, want <= 4000", len(dashOutput))
	}
	if kb == nil {
		t.Errorf("expected non-nil keyboard")
	}

	// 3. Verify formatModelsSummary
	summary := formatModelsSummary(largeModels, 3)
	if len(summary) > 300 {
		t.Errorf("formatModelsSummary output too long: got %d chars", len(summary))
	}

	emptySummary := formatModelsSummary(nil, 3)
	if emptySummary != "(default only)" {
		t.Errorf("expected '(default only)', got '%s'", emptySummary)
	}
}
