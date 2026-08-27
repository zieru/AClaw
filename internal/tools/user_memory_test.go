package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"goassistant/internal/storage"
)

type testMemoryManager struct {
	db *storage.DB
}

func (m *testMemoryManager) UpsertFact(scope, scopeID, key, content, category string) error {
	return m.db.UpsertMemoryItem(scope, scopeID, key, content, category)
}

func (m *testMemoryManager) ListMemories(scope, scopeID string) ([]storage.MemoryRecord, error) {
	return m.db.ListMemoryItems(scope, scopeID)
}

func (m *testMemoryManager) SearchMemories(scope, scopeID, query string) ([]storage.MemoryRecord, error) {
	return m.db.SearchMemoryItems(scope, scopeID, query)
}

func (m *testMemoryManager) DeleteMemoryItem(id string) error {
	return m.db.DeleteMemoryItem(id)
}

func (m *testMemoryManager) DeleteMemoryByKey(scope, scopeID, key string) error {
	return m.db.DeleteMemoryByKey(scope, scopeID, key)
}

func (m *testMemoryManager) ClearUserMemory(userID string) error {
	items, err := m.db.ListMemoryItems("user", userID)
	if err != nil {
		return err
	}
	for _, it := range items {
		_ = m.db.DeleteMemoryItem(it.ID)
	}
	return nil
}

func (m *testMemoryManager) ClearChannelMemory(channelID string) error {
	items, err := m.db.ListMemoryItems("channel", channelID)
	if err != nil {
		return err
	}
	for _, it := range items {
		_ = m.db.DeleteMemoryItem(it.ID)
	}
	return nil
}

func TestUserMemoryTool(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_memory.db")

	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	mgr := &testMemoryManager{db: db}
	tool := NewUserMemoryTool(mgr)

	ctx := context.WithValue(context.Background(), "user_id", "user_123")
	ctx = context.WithValue(ctx, "channel_id", "chan_456")

	// 1. Save memory
	saveRes, err := tool.Execute(ctx, map[string]interface{}{
		"action":   "save",
		"key":      "makanan_favorit",
		"content":  "Nasi Goreng Spesial Pedas",
		"category": "preference",
	})
	if err != nil {
		t.Fatalf("unexpected save error: %v", err)
	}
	if !strings.Contains(saveRes, "Memori Berhasil Disimpan") {
		t.Errorf("expected success message, got: %s", saveRes)
	}

	// 2. Search memory
	searchRes, err := tool.Execute(ctx, map[string]interface{}{
		"action": "search",
		"query":  "Goreng",
	})
	if err != nil {
		t.Fatalf("unexpected search error: %v", err)
	}
	if !strings.Contains(searchRes, "Nasi Goreng") {
		t.Errorf("expected search result to contain 'Nasi Goreng', got: %s", searchRes)
	}

	// 3. List memories
	listRes, err := tool.Execute(ctx, map[string]interface{}{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("unexpected list error: %v", err)
	}
	if !strings.Contains(listRes, "makanan_favorit") {
		t.Errorf("expected list result to contain 'makanan_favorit', got: %s", listRes)
	}

	// 4. Delete memory by key
	delRes, err := tool.Execute(ctx, map[string]interface{}{
		"action": "delete",
		"key":    "makanan_favorit",
	})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}
	if !strings.Contains(delRes, "Berhasil Dihapus") {
		t.Errorf("expected delete success, got: %s", delRes)
	}

	// 5. Verify list is empty
	emptyList, err := tool.Execute(ctx, map[string]interface{}{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("unexpected list error: %v", err)
	}
	if !strings.Contains(emptyList, "Belum ada catatan") {
		t.Errorf("expected empty list message, got: %s", emptyList)
	}
}
