package admin

import (
	"path/filepath"
	"strings"
	"testing"

	"goassistant/internal/config"
	"goassistant/internal/memory"
	"goassistant/internal/storage"
)

func TestAdminMemoryUI_Submenus(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_admin_mem.db")

	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer db.Close()

	memCfg := config.MemoryConfig{
		Enabled:                 true,
		AutoExtract:             true,
		Strategy:                "hybrid",
		MaxTokens:               2000,
		MaxContextItems:         20,
		RetentionDays:           30,
		PromotionThreshold:      3,
		AutoCompaction:          true,
		CompactionIntervalHours: 24,
		CompactionThreshold:     100,
		SimilarityThreshold:     0.60,
		Embedding: config.EmbeddingConfig{
			Enabled:  true,
			Provider: "openai",
			Model:    "text-embedding-3-small",
		},
	}

	mm := memory.NewManager(db, memCfg, nil, nil)
	memUI := NewMemoryUI(db, mm, nil)

	// 1. Dashboard
	dashTxt, dashMarkup := memUI.RenderMemoryDashboard("399999658")
	if !strings.Contains(dashTxt, "PUSAT KONTROL & PENGATURAN MEMORY ENGINE") {
		t.Fatalf("unexpected dashboard text: %s", dashTxt)
	}
	if dashMarkup == nil || len(dashMarkup.InlineKeyboard) < 3 {
		t.Fatalf("expected at least 3 rows in dashboard markup")
	}

	// 2. General Menu
	genTxt, genMarkup := memUI.RenderMemoryGeneralMenu("399999658")
	if !strings.Contains(genTxt, "PENGATURAN UMUM & EKSTRAKSI MEMORY") {
		t.Fatalf("unexpected general menu text: %s", genTxt)
	}
	if genMarkup == nil || len(genMarkup.InlineKeyboard) < 3 {
		t.Fatalf("expected at least 3 rows in general markup")
	}

	// 3. Strategy Menu
	stratTxt, stratMarkup := memUI.RenderMemoryStrategyMenu("399999658")
	if !strings.Contains(stratTxt, "PENGATURAN STRATEGI & BUDGET TOKEN") {
		t.Fatalf("unexpected strategy menu text: %s", stratTxt)
	}
	if stratMarkup == nil || len(stratMarkup.InlineKeyboard) < 4 {
		t.Fatalf("expected at least 4 rows in strategy markup")
	}

	// 4. Retention Menu
	retTxt, retMarkup := memUI.RenderMemoryRetentionMenu("399999658")
	if !strings.Contains(retTxt, "PENGATURAN RETENSI & PROMOSI MEMORI") {
		t.Fatalf("unexpected retention menu text: %s", retTxt)
	}
	if retMarkup == nil || len(retMarkup.InlineKeyboard) < 3 {
		t.Fatalf("expected at least 3 rows in retention markup")
	}

	// 5. Compaction Menu
	compTxt, compMarkup := memUI.RenderMemoryCompactionMenu("399999658")
	if !strings.Contains(compTxt, "PENGATURAN AUTO-COMPACTION & PEMBERSIHAN") {
		t.Fatalf("unexpected compaction menu text: %s", compTxt)
	}
	if compMarkup == nil || len(compMarkup.InlineKeyboard) < 4 {
		t.Fatalf("expected at least 4 rows in compaction markup")
	}

	// 6. Embedding Menu
	embTxt, embMarkup := memUI.RenderMemoryEmbeddingMenu("399999658")
	if !strings.Contains(embTxt, "PENGATURAN REMOTE VECTOR EMBEDDING") {
		t.Fatalf("unexpected embedding menu text: %s", embTxt)
	}
	if embMarkup == nil || len(embMarkup.InlineKeyboard) < 4 {
		t.Fatalf("expected at least 4 rows in embedding markup")
	}

	// 7. Data Menu
	dataTxt, dataMarkup := memUI.RenderMemoryDataMenu("399999658")
	if !strings.Contains(dataTxt, "KELOLA DATA & CATATAN MEMORI") {
		t.Fatalf("unexpected data menu text: %s", dataTxt)
	}
	if dataMarkup == nil || len(dataMarkup.InlineKeyboard) < 3 {
		t.Fatalf("expected at least 3 rows in data markup")
	}

	// 8. Model Picker
	pickerTxt, pickerMarkup := memUI.RenderModelPicker()
	if !strings.Contains(pickerTxt, "DETEKSI MODEL EMBEDDING GOOGLE RESMI") {
		t.Fatalf("unexpected model picker text: %s", pickerTxt)
	}
	if pickerMarkup == nil || len(pickerMarkup.InlineKeyboard) < 1 {
		t.Fatalf("expected at least 1 row in model picker markup")
	}
}
