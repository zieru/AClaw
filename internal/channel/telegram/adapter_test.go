package telegram

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"goassistant/internal/agent"
	"goassistant/internal/config"
	"goassistant/internal/memory"
	"goassistant/internal/storage"
)

func TestExtractInteractiveOptions(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectedTxt string
		expectedOpt []string
	}{
		{
			name:        "No options",
			input:       "Ini adalah jawaban biasa tanpa opsi.",
			expectedTxt: "Ini adalah jawaban biasa tanpa opsi.",
			expectedOpt: nil,
		},
		{
			name: "Standard pipe format",
			input: `Berikut adalah ringkasannya.
Apakah ada yang ingin ditindaklanjuti?

[OPSI: 📊 Tampilkan Grafik | 📑 Ekspor Laporan | 🔍 Analisis Lanjutan]`,
			expectedTxt: "Berikut adalah ringkasannya.\nApakah ada yang ingin ditindaklanjuti?",
			expectedOpt: []string{"📊 Tampilkan Grafik", "📑 Ekspor Laporan", "🔍 Analisis Lanjutan"},
		},
		{
			name: "Numbered format with OPTIONS tag",
			input: `Pilih salah satu langkah:
[OPTIONS:
1. Opsi Pertama
2. Opsi Kedua
]`,
			expectedTxt: "Pilih salah satu langkah:",
			expectedOpt: []string{"Opsi Pertama", "Opsi Kedua"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanText, opts := extractInteractiveOptions(tt.input)
			if cleanText != tt.expectedTxt {
				t.Errorf("cleanText = %q, want %q", cleanText, tt.expectedTxt)
			}
			if !reflect.DeepEqual(opts, tt.expectedOpt) {
				t.Errorf("opts = %v, want %v", opts, tt.expectedOpt)
			}
		})
	}
}

func TestTelegramMemorySubmenus(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_tg_mem.db")

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
	orch := agent.NewOrchestrator(db, nil, mm, nil, nil, nil)

	adapter := &BotAdapter{
		channelID:    "test-chan",
		name:         "TestBot",
		db:           db,
		orchestrator: orch,
	}

	// 1. Dashboard
	dashTxt, dashMarkup := adapter.renderMemoryDashboard("123456")
	if !strings.Contains(dashTxt, "PUSAT KONTROL & PENGATURAN MEMORY ENGINE") {
		t.Fatalf("unexpected dashboard text: %s", dashTxt)
	}
	if dashMarkup == nil || len(dashMarkup.InlineKeyboard) < 3 {
		t.Fatalf("expected at least 3 rows in dashboard markup")
	}

	// 2. General Menu
	genTxt, genMarkup := adapter.renderMemoryGeneralMenu("123456")
	if !strings.Contains(genTxt, "PENGATURAN UMUM & EKSTRAKSI MEMORY") {
		t.Fatalf("unexpected general menu text: %s", genTxt)
	}
	if genMarkup == nil || len(genMarkup.InlineKeyboard) < 3 {
		t.Fatalf("expected at least 3 rows in general markup")
	}

	// 3. Strategy Menu
	stratTxt, stratMarkup := adapter.renderMemoryStrategyMenu("123456")
	if !strings.Contains(stratTxt, "PENGATURAN STRATEGI & BUDGET TOKEN") {
		t.Fatalf("unexpected strategy menu text: %s", stratTxt)
	}
	if stratMarkup == nil || len(stratMarkup.InlineKeyboard) < 4 {
		t.Fatalf("expected at least 4 rows in strategy markup")
	}

	// 4. Retention Menu
	retTxt, retMarkup := adapter.renderMemoryRetentionMenu("123456")
	if !strings.Contains(retTxt, "PENGATURAN RETENSI & PROMOSI MEMORI") {
		t.Fatalf("unexpected retention menu text: %s", retTxt)
	}
	if retMarkup == nil || len(retMarkup.InlineKeyboard) < 3 {
		t.Fatalf("expected at least 3 rows in retention markup")
	}

	// 5. Compaction Menu
	compTxt, compMarkup := adapter.renderMemoryCompactionMenu("123456")
	if !strings.Contains(compTxt, "PENGATURAN AUTO-COMPACTION & PEMBERSIHAN") {
		t.Fatalf("unexpected compaction menu text: %s", compTxt)
	}
	if compMarkup == nil || len(compMarkup.InlineKeyboard) < 4 {
		t.Fatalf("expected at least 4 rows in compaction markup")
	}

	// 6. Embedding Menu
	embTxt, embMarkup := adapter.renderMemoryEmbeddingMenu("123456")
	if !strings.Contains(embTxt, "PENGATURAN REMOTE VECTOR EMBEDDING") {
		t.Fatalf("unexpected embedding menu text: %s", embTxt)
	}
	if embMarkup == nil || len(embMarkup.InlineKeyboard) < 4 {
		t.Fatalf("expected at least 4 rows in embedding markup")
	}

	// 7. Data Menu
	dataTxt, dataMarkup := adapter.renderMemoryDataMenu("123456")
	if !strings.Contains(dataTxt, "KELOLA DATA & CATATAN MEMORI") {
		t.Fatalf("unexpected data menu text: %s", dataTxt)
	}
	if dataMarkup == nil || len(dataMarkup.InlineKeyboard) < 3 {
		t.Fatalf("expected at least 3 rows in data markup")
	}

	// 8. Model Picker
	pickerTxt, pickerMarkup := adapter.renderModelPicker()
	if !strings.Contains(pickerTxt, "DETEKSI MODEL EMBEDDING GOOGLE RESMI") {
		t.Fatalf("unexpected model picker text: %s", pickerTxt)
	}
	if pickerMarkup == nil || len(pickerMarkup.InlineKeyboard) < 1 {
		t.Fatalf("expected at least 1 row in model picker markup")
	}
}
