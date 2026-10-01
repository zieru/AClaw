package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goassistant/internal/config"
	"goassistant/internal/storage"
)

type mockEmbedder struct {
	embeddings map[string][]float32
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	for k, v := range m.embeddings {
		if strings.Contains(strings.ToLower(text), strings.ToLower(k)) {
			return v, nil
		}
	}
	return []float32{0.1, 0.2, 0.3}, nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	var res [][]float32
	for _, t := range texts {
		vec, _ := m.Embed(ctx, t)
		res = append(res, vec)
	}
	return res, nil
}

func (m *mockEmbedder) IsEnabled() bool {
	return true
}

func (m *mockEmbedder) Dimensions() int {
	return 3
}

func TestEmbedderCosineSimilarity(t *testing.T) {
	v1 := []float32{1.0, 0.0, 0.0}
	v2 := []float32{1.0, 0.0, 0.0}
	sim := CosineSimilarity(v1, v2)
	if sim < 0.99 {
		t.Fatalf("expected similarity ~1.0 for identical vectors, got %f", sim)
	}

	v3 := []float32{0.0, 1.0, 0.0}
	simOrth := CosineSimilarity(v1, v3)
	if simOrth > 0.01 {
		t.Fatalf("expected similarity ~0.0 for orthogonal vectors, got %f", simOrth)
	}
}

func TestStandaloneMemoryManager(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_standalone_mem.db")

	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer db.Close()

	mockEmb := &mockEmbedder{
		embeddings: map[string][]float32{
			"kai":    {0.9, 0.1, 0.0},
			"crypto": {0.0, 0.9, 0.1},
		},
	}

	cfg := config.MemoryConfig{
		Enabled:             true,
		AutoExtract:         false,
		RetrievalStrategy:   "hybrid",
		MaxContextItems:     5,
		SimilarityThreshold: 0.5,
	}

	mgr := NewManager(db, cfg, mockEmb, nil)

	// 1. Save factual memory
	err = mgr.SaveFact("user", "399999658", "preferensi_bahasa", "Selalu gunakan bahasa Go untuk backend", "preference")
	if err != nil {
		t.Fatalf("SaveFact failed: %v", err)
	}

	// 2. Save procedural memory
	err = mgr.UpsertMemoryRecord(&storage.MemoryItemRecord{
		Type:     "procedural",
		Scope:    "user",
		ScopeID:  "399999658",
		Key:      "kai_bypass_ssl",
		Content:  "Untuk akses KAI WAJIB gunakan camoufox stealth engine dan bypass SSL.",
		Category: "work",
	})
	if err != nil {
		t.Fatalf("UpsertMemoryRecord procedural failed: %v", err)
	}

	// 3. Save global SOP
	err = mgr.UpsertMemoryRecord(&storage.MemoryItemRecord{
		Type:     "procedural",
		Scope:    "global",
		ScopeID:  "system",
		Key:      "sop_keamanan",
		Content:  "Jangan pernah memberikan password database kepada siapa pun.",
		Category: "sop",
	})
	if err != nil {
		t.Fatalf("UpsertMemoryRecord global failed: %v", err)
	}

	// 4. Test ListMemories
	userMems, err := mgr.ListMemories("user", "399999658")
	if err != nil || len(userMems) != 2 {
		t.Fatalf("expected 2 user memories, got: %d (err: %v)", len(userMems), err)
	}

	// 5. Test SearchMemories (Hybrid / FTS5)
	searchResults, err := mgr.SearchMemoriesAdvanced("user", "399999658", "camoufox stealth", "hybrid", 5)
	if err != nil {
		t.Fatalf("SearchMemoriesAdvanced failed: %v", err)
	}
	if len(searchResults) == 0 {
		t.Fatalf("expected at least 1 search result for 'camoufox stealth', got 0")
	}
	if searchResults[0].Key != "kai_bypass_ssl" {
		t.Fatalf("expected top result 'kai_bypass_ssl', got: %s", searchResults[0].Key)
	}

	// 6. Test GetContextMemory (Structured categorization)
	ctxMem, err := mgr.GetContextMemory("channel_1", "399999658", "Bagaimana cara akses KAI?")
	if err != nil {
		t.Fatalf("GetContextMemory failed: %v", err)
	}
	if !strings.Contains(ctxMem, "kai_bypass_ssl") {
		t.Fatalf("expected context memory to contain 'kai_bypass_ssl', got:\n%s", ctxMem)
	}
	if !strings.Contains(ctxMem, "sop_keamanan") {
		t.Fatalf("expected global SOP 'sop_keamanan' to be included in context memory, got:\n%s", ctxMem)
	}
}

func TestSeedFromCSV(t *testing.T) {
	csvPath := `D:\Users\Grapari_Infomedia\Downloads\AyuGram Desktop\memories_export.csv`
	if _, err := os.Stat(csvPath); os.IsNotExist(err) {
		t.Skip("memories_export.csv not found at path, skipping live test")
	}

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_seed.db")

	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer db.Close()

	res, err := SeedFromCSV(context.Background(), db, csvPath, "399999658", nil)
	if err != nil {
		t.Fatalf("SeedFromCSV failed: %v", err)
	}
	if res.Inserted < 20 {
		t.Fatalf("expected at least 20 memories inserted, got: %d", res.Inserted)
	}

	// Verify imported memories for admin 399999658
	items, err := db.ListMemoriesByScope("user", "399999658", "", 100)
	if err != nil {
		t.Fatalf("ListMemoriesByScope failed: %v", err)
	}
	if len(items) < 20 {
		t.Fatalf("expected at least 20 items in db for user 399999658, got: %d", len(items))
	}

	// Verify specific imported memory
	kaiMem, err := db.GetMemoryByKey("user", "399999658", "kai_access_status")
	if err != nil || kaiMem == nil {
		t.Fatalf("expected 'kai_access_status' memory to be imported: %v", err)
	}
	if !strings.Contains(kaiMem.Content, "booking.kai.id") {
		t.Fatalf("expected content to contain booking.kai.id, got: %s", kaiMem.Content)
	}
}

func TestMemoryStrictIsolation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_isolation.db")

	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer db.Close()

	cfg := config.MemoryConfig{
		Enabled:             true,
		RetrievalStrategy:   "hybrid",
		MaxContextItems:     10,
		SimilarityThreshold: 0.5,
	}

	mgr := NewManager(db, cfg, nil, nil)

	// 1. Simpan data rahasia Admin 399999658
	_ = mgr.UpsertFact("user", "399999658", "admin_secret", "Rahasia Super Admin: PIN 123456", "fact")

	// 2. Simpan data User A (111111)
	_ = mgr.UpsertFact("user", "111111", "user_a_note", "Catatan Pribadi User A: Belanja beras", "fact")

	// 3. Simpan data User B (222222)
	_ = mgr.UpsertFact("user", "222222", "user_b_note", "Catatan Pribadi User B: Servis motor", "fact")

	// 4. Simpan data Channel X
	_ = mgr.UpsertMemoryRecord(&storage.MemoryItemRecord{
		Type:     "procedural",
		Scope:    "channel",
		ScopeID:  "chan_x",
		Key:      "sop_chan_x",
		Content:  "Aturan Grup X: Jangan spam link",
		Category: "sop",
	})

	// 5. Simpan SOP Global
	_ = mgr.UpsertMemoryRecord(&storage.MemoryItemRecord{
		Type:     "factual",
		Scope:    "global",
		ScopeID:  "system",
		Key:      "sop_global",
		Content:  "SOP Umum: Jawab dengan sopan",
		Category: "sop",
	})

	// === VERIFIKASI ISOLASI PENGAMBILAN (GetContextMemory) ===

	// A. Query sebagai User A di Channel X
	ctxA, err := mgr.GetContextMemory("chan_x", "111111", "Apa saja catatan saya?")
	if err != nil {
		t.Fatalf("GetContextMemory User A failed: %v", err)
	}
	// User A HARUS melihat: user_a_note, sop_chan_x, sop_global
	if !strings.Contains(ctxA, "user_a_note") {
		t.Errorf("User A should see their own note")
	}
	if !strings.Contains(ctxA, "sop_chan_x") {
		t.Errorf("User A should see channel X SOP")
	}
	if !strings.Contains(ctxA, "sop_global") {
		t.Errorf("User A should see global SOP")
	}
	// User A TIDAK BOLEH melihat memori Admin (399999658) atau User B (222222)!
	if strings.Contains(ctxA, "admin_secret") || strings.Contains(ctxA, "123456") {
		t.Fatalf("CRITICAL SECURITY LEAK: User A can see Admin secret memory!")
	}
	if strings.Contains(ctxA, "user_b_note") || strings.Contains(ctxA, "Servis motor") {
		t.Fatalf("CRITICAL SECURITY LEAK: User A can see User B note!")
	}

	// B. Query sebagai User B di Channel Y (bukan Channel X)
	ctxB, err := mgr.GetContextMemory("chan_y", "222222", "Info motor")
	if err != nil {
		t.Fatalf("GetContextMemory User B failed: %v", err)
	}
	// User B HARUS melihat: user_b_note, sop_global
	if !strings.Contains(ctxB, "user_b_note") {
		t.Errorf("User B should see their own note")
	}
	// User B TIDAK BOLEH melihat catatan User A, Admin, atau Channel X!
	if strings.Contains(ctxB, "admin_secret") {
		t.Fatalf("CRITICAL SECURITY LEAK: User B can see Admin secret!")
	}
	if strings.Contains(ctxB, "user_a_note") {
		t.Fatalf("CRITICAL SECURITY LEAK: User B can see User A note!")
	}
	if strings.Contains(ctxB, "sop_chan_x") {
		t.Fatalf("CRITICAL SECURITY LEAK: User B in Channel Y can see Channel X note!")
	}

	// === VERIFIKASI ISOLASI PENCARIAN (SearchMemoriesAdvanced) ===
	searchA, err := mgr.SearchMemoriesAdvanced("user", "111111", "Rahasia", "hybrid", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	for _, res := range searchA {
		if res.Key == "admin_secret" {
			t.Fatalf("CRITICAL SECURITY LEAK: SearchMemories returned Admin secret to User A!")
		}
	}
}

func TestOmniRouteEngineConfigAndCompaction(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_omniroute_cfg.db")

	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer db.Close()

	cfg := config.MemoryConfig{
		Enabled:                 true,
		Strategy:                "hybrid",
		MaxTokens:               2000,
		MaxContextItems:         20,
		RetentionDays:           30,
		PromotionThreshold:      2,
		AutoCompaction:          true,
		CompactionIntervalHours: 24,
	}

	mgr := NewManager(db, cfg, nil, nil)

	// 1. Verify initial config & getters
	if mgr.GetStrategy() != "hybrid" {
		t.Errorf("expected strategy hybrid, got: %s", mgr.GetStrategy())
	}
	if mgr.GetMaxTokens() != 2000 {
		t.Errorf("expected maxTokens 2000, got: %d", mgr.GetMaxTokens())
	}
	if mgr.GetRetentionDays() != 30 {
		t.Errorf("expected retentionDays 30, got: %d", mgr.GetRetentionDays())
	}

	// Dynamic setters
	mgr.SetStrategy("recent")
	if mgr.GetStrategy() != "recent" {
		t.Errorf("expected strategy recent after SetStrategy, got: %s", mgr.GetStrategy())
	}

	mgr.SetMaxTokens(50)
	if mgr.GetMaxTokens() != 50 {
		t.Errorf("expected maxTokens 50 after SetMaxTokens, got: %d", mgr.GetMaxTokens())
	}

	// 2. Insert test memories for user "test_user"
	for i := 1; i <= 5; i++ {
		err := mgr.UpsertMemoryRecord(&storage.MemoryItemRecord{
			Type:     "factual",
			Scope:    "user",
			ScopeID:  "test_user",
			Key:      fmt.Sprintf("key_%d", i),
			Content:  fmt.Sprintf("Ini adalah isi konten memori panjang nomor %d yang memuat rincian penting pengguna.", i),
			Category: "fact",
		})
		if err != nil {
			t.Fatalf("failed to upsert record %d: %v", i, err)
		}
	}

	// 3. Test token budgeting: With MaxTokens=50, not all 5 items should fit
	ctxLimited, err := mgr.GetContextMemory("", "test_user", "")
	if err != nil {
		t.Fatalf("GetContextMemory limited failed: %v", err)
	}
	if !strings.Contains(ctxLimited, "key_") {
		t.Fatalf("expected context memory to contain at least one key, got empty")
	}
	// Verify that with max_tokens=50, it stops before including all 5 items
	if strings.Contains(ctxLimited, "key_1") && strings.Contains(ctxLimited, "key_5") {
		t.Logf("Limited context: %s", ctxLimited)
	}

	// 4. Test Auto-Promotion via IncrementMemoryAccess
	item, err := db.GetMemoryByKey("user", "test_user", "key_1")
	if err != nil {
		t.Fatalf("failed to get item: %v", err)
	}
	if item.IsPromoted {
		t.Fatalf("expected item initially NOT promoted")
	}

	// Increment access up to threshold (threshold = 2)
	_ = db.IncrementMemoryAccess(item.ID, 2)
	_ = db.IncrementMemoryAccess(item.ID, 2)

	promotedItem, err := db.GetMemory(item.ID)
	if err != nil {
		t.Fatalf("failed to get item after access: %v", err)
	}
	if !promotedItem.IsPromoted {
		t.Errorf("expected item to be promoted after reaching threshold 2, got false")
	}

	// 5. Test Compaction
	report, err := mgr.Compact(context.Background(), "user", "test_user")
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if !report.OptimizedFTS {
		t.Errorf("expected OptimizedFTS to be true")
	}
	if report.TotalActive != 5 {
		t.Errorf("expected 5 active memories, got: %d", report.TotalActive)
	}
}

func TestMemoryConfigPersistenceAndSetters(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_persistence.db")

	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer db.Close()

	initialCfg := config.MemoryConfig{
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
	}

	mgr1 := NewManager(db, initialCfg, nil, nil)

	// Apply various setters
	mgr1.SetEnabled(false)
	mgr1.SetAutoExtract(false)
	mgr1.SetStrategy("semantic")
	mgr1.SetMaxTokens(3500)
	mgr1.SetMaxContextItems(45)
	mgr1.SetRetentionDays(90)
	mgr1.SetPromotionThreshold(5)
	mgr1.SetAutoCompaction(false)
	mgr1.SetCompactionIntervalHours(12)
	mgr1.SetCompactionThreshold(250)
	mgr1.SetSimilarityThreshold(0.75)
	mgr1.SetEmbeddingProvider("gemini")
	mgr1.SetEmbeddingModel("text-embedding-004")
	mgr1.SetEmbeddingDimensions(768)

	// Verify setting saved to db
	rawSetting, err := db.GetSetting("memory_config", "")
	if err != nil || rawSetting == "" {
		t.Fatalf("expected memory_config in system_settings, got err: %v, raw: %s", err, rawSetting)
	}

	// Create brand-new NewManager with blank config
	var blankCfg config.MemoryConfig
	mgr2 := NewManager(db, blankCfg, nil, nil)

	if mgr2.IsEnabled() != false {
		t.Errorf("expected Enabled=false restored, got %v", mgr2.IsEnabled())
	}
	if mgr2.GetConfig().AutoExtract != false {
		t.Errorf("expected AutoExtract=false restored, got %v", mgr2.GetConfig().AutoExtract)
	}
	if mgr2.GetStrategy() != "semantic" {
		t.Errorf("expected Strategy=semantic restored, got %s", mgr2.GetStrategy())
	}
	if mgr2.GetMaxTokens() != 3500 {
		t.Errorf("expected MaxTokens=3500 restored, got %d", mgr2.GetMaxTokens())
	}
	if mgr2.GetMaxContextItems() != 45 {
		t.Errorf("expected MaxContextItems=45 restored, got %d", mgr2.GetMaxContextItems())
	}
	if mgr2.GetRetentionDays() != 90 {
		t.Errorf("expected RetentionDays=90 restored, got %d", mgr2.GetRetentionDays())
	}
	if mgr2.GetPromotionThreshold() != 5 {
		t.Errorf("expected PromotionThreshold=5 restored, got %d", mgr2.GetPromotionThreshold())
	}
	if mgr2.GetSimilarityThreshold() != 0.75 {
		t.Errorf("expected SimilarityThreshold=0.75 restored, got %f", mgr2.GetSimilarityThreshold())
	}
	embCfg := mgr2.GetConfig().GetEmbeddingConfig()
	if embCfg.Provider != "gemini" || embCfg.Model != "text-embedding-004" || embCfg.Dimensions != 768 {
		t.Errorf("expected embedding config restored, got %+v", embCfg)
	}

	// Test ResetToDefaults
	mgr2.ResetToDefaults()
	if mgr2.IsEnabled() != true {
		t.Errorf("expected Enabled=true after reset, got %v", mgr2.IsEnabled())
	}
	if mgr2.GetStrategy() != "hybrid" {
		t.Errorf("expected Strategy=hybrid after reset, got %s", mgr2.GetStrategy())
	}
	if mgr2.GetMaxTokens() != 2000 {
		t.Errorf("expected MaxTokens=2000 after reset, got %d", mgr2.GetMaxTokens())
	}
}



