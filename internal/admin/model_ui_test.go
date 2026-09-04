package admin

import (
	"path/filepath"
	"testing"

	"goassistant/internal/provider"
	"goassistant/internal/storage"
)

func setupTestModelUI(t *testing.T) (*ModelUI, *storage.DB, *provider.Manager) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_model_ui.db")

	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	pm := provider.GetManager()

	// Register a test combo
	pm.RegisterCombo(&storage.ModelComboRecord{
		Name:     "combo_default",
		Strategy: "failsafe",
		IsActive: true,
		Targets: []storage.ComboTarget{
			{ProviderID: "testprov", Model: "test-model-1"},
			{ProviderID: "testprov", Model: "test-model-2"},
		},
	})

	ui := NewModelUI(db, pm)
	return ui, db, pm
}

func TestModelUISessionManagement(t *testing.T) {
	ui, _, _ := setupTestModelUI(t)
	userID := int64(12345)

	if sess := ui.getSession(userID); sess != nil {
		t.Errorf("expected nil session initially, got %+v", sess)
	}

	ui.setSession(userID, ModelUIStepPickCombo, "", 0)
	sess := ui.getSession(userID)
	if sess == nil {
		t.Fatalf("expected active session, got nil")
	}
	if sess.Step != ModelUIStepPickCombo {
		t.Errorf("expected step %v, got %v", ModelUIStepPickCombo, sess.Step)
	}

	ui.CancelSession(userID)
	if sess := ui.getSession(userID); sess != nil {
		t.Errorf("expected nil session after CancelSession, got %+v", sess)
	}
}

func TestModelUISaveOverride(t *testing.T) {
	ui, db, _ := setupTestModelUI(t)
	scope := "chat"
	chatID := "1001"

	// Save combo override
	msg, err := ui.saveModelOverride(scope, chatID, "combo_default")
	if err != nil {
		t.Fatalf("unexpected error saving override: %v", err)
	}
	if msg == "" {
		t.Errorf("expected non-empty success message")
	}

	pol := db.GetResolvedPolicy("admin", chatID)
	if pol.ModelOverride != "combo_default" {
		t.Errorf("expected ModelOverride 'combo_default', got '%s'", pol.ModelOverride)
	}

	// Reset to default
	msg, err = ui.saveModelOverride(scope, chatID, "")
	if err != nil {
		t.Fatalf("unexpected error resetting override: %v", err)
	}
	pol = db.GetResolvedPolicy("admin", chatID)
	if pol.ModelOverride != "" {
		t.Errorf("expected empty ModelOverride, got '%s'", pol.ModelOverride)
	}
}

func TestGetAllModelsForProviderDefaultFirst(t *testing.T) {
	ui, _, _ := setupTestModelUI(t)

	// Create a provider where default model is "gpt-4o-mini" (starts with g)
	// and other models include "anthropic/claude-3-5-sonnet", "ai21/jamba", "deepseek/deepseek-chat", "z-model"
	p := provider.NewOpenAIProviderWithKeys(
		"test_provider",
		"openai",
		"https://api.openai.com/v1",
		[]string{"sk-test"},
		"round-robin",
		"gpt-4o-mini", // Default model
		[]string{
			"z-model",
			"anthropic/claude-3-5-sonnet",
			"gpt-4o-mini",
			"ai21/jamba",
			"deepseek/deepseek-chat",
		},
	)

	models := ui.getAllModelsForProvider(p)
	if len(models) != 5 {
		t.Fatalf("expected 5 models, got %d: %v", len(models), models)
	}

	// First model MUST be the default model
	if models[0] != "gpt-4o-mini" {
		t.Errorf("expected models[0] to be default model 'gpt-4o-mini', got '%s'", models[0])
	}

	// Remaining models must be sorted alphabetically
	expectedRemaining := []string{
		"ai21/jamba",
		"anthropic/claude-3-5-sonnet",
		"deepseek/deepseek-chat",
		"z-model",
	}
	for i, exp := range expectedRemaining {
		if models[i+1] != exp {
			t.Errorf("expected models[%d] to be '%s', got '%s'", i+1, exp, models[i+1])
		}
	}
}

