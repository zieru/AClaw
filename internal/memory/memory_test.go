package memory

import (
	"context"
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

