package admin

import (
	"path/filepath"
	"testing"

	"goassistant/internal/provider"
	"goassistant/internal/storage"
)

func TestComboWizard_GetModelsForProvider(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_combo_wiz.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	pm := provider.GetManager()
	cw := NewComboWizard(db, pm, nil)

	p := &storage.ProviderRecord{
		ID:           "test_prov",
		Name:         "TestProv",
		DefaultModel: "gpt-4o-mini",
		Models: []string{
			"z-model",
			"claude-3-5-sonnet",
			"gpt-4o-mini",
			"deepseek-chat",
			"alpha-model",
		},
	}

	models := cw.getModelsForProvider(p)
	if len(models) != 5 {
		t.Fatalf("expected 5 models, got %d: %v", len(models), models)
	}

	// 1. Default model must be at index 0
	if models[0] != "gpt-4o-mini" {
		t.Errorf("expected models[0] to be default model 'gpt-4o-mini', got '%s'", models[0])
	}

	// 2. Remaining models must be sorted alphabetically
	expectedRest := []string{
		"alpha-model",
		"claude-3-5-sonnet",
		"deepseek-chat",
		"z-model",
	}
	for i, exp := range expectedRest {
		if models[i+1] != exp {
			t.Errorf("expected models[%d] to be '%s', got '%s'", i+1, exp, models[i+1])
		}
	}
}

func TestComboWizard_TargetAddition(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_combo_wiz_target.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	pm := provider.GetManager()
	cw := NewComboWizard(db, pm, nil)

	p := &storage.ProviderRecord{
		ID:           "prov_1",
		Name:         "Provider 1",
		DefaultModel: "gpt-4o",
		Models:       []string{"gpt-4o", "gpt-4o-mini"},
	}

	sess := &ComboWizardSession{
		Name:             "test_combo",
		SelectedProvider: p,
		Step:             StepComboPickModel,
	}

	// Apply model by custom name
	err = cw.applySelectedModel(nil, sess, "custom-model-x")
	if err != nil {
		// promptPickProvider might fail because tele.Context is nil, but targets should be updated
	}

	if len(sess.Targets) != 1 {
		t.Fatalf("expected 1 target in session, got %d", len(sess.Targets))
	}
	if sess.Targets[0].Model != "custom-model-x" {
		t.Errorf("expected target model 'custom-model-x', got '%s'", sess.Targets[0].Model)
	}
	if sess.Targets[0].ProviderID != "prov_1" {
		t.Errorf("expected target provider 'prov_1', got '%s'", sess.Targets[0].ProviderID)
	}
}
