package admin

import (
	"path/filepath"
	"strings"
	"testing"

	"goassistant/internal/memory"
	"goassistant/internal/storage"
)

func TestTopicUI_DashboardAndOperations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_topic_ui.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	sm := memory.NewSessionManager(db)
	topicUI := NewTopicUI(db, sm, nil)

	channelID := "admin"
	chatID := "chat_999"
	userID := "user_888"

	// 1. Initial dashboard rendering
	txt, menu, err := topicUI.RenderTopicDashboard(channelID, chatID, userID)
	if err != nil {
		t.Fatalf("failed to render dashboard: %v", err)
	}
	if !strings.Contains(txt, "MANAJEMEN TOPIK PERCAKAPAN") {
		t.Errorf("expected dashboard title, got: %s", txt)
	}
	if !strings.Contains(txt, "Topik Utama") {
		t.Errorf("expected default 'Topik Utama', got: %s", txt)
	}
	if menu == nil || len(menu.InlineKeyboard) == 0 {
		t.Errorf("expected non-empty inline keyboard")
	}

	// 2. Create second topic via sessionManager
	t2, err := sm.CreateNewTopic(channelID, chatID, userID, "Analisis Log OneDrive")
	if err != nil {
		t.Fatalf("failed to create topic: %v", err)
	}

	txt2, _, _ := topicUI.RenderTopicDashboard(channelID, chatID, userID)
	if !strings.Contains(txt2, "Analisis Log OneDrive") {
		t.Errorf("expected topic 2 in dashboard, got: %s", txt2)
	}
	if !strings.Contains(txt2, "🟢 <b>Analisis Log OneDrive</b>") {
		t.Errorf("expected topic 2 to be active, got: %s", txt2)
	}

	// 3. Rename Topic
	err = sm.RenameTopic(t2.ID, "OneDrive Analitik & Log")
	if err != nil {
		t.Fatalf("failed to rename topic: %v", err)
	}
	txt3, _, _ := topicUI.RenderTopicDashboard(channelID, chatID, userID)
	if !strings.Contains(txt3, "OneDrive Analitik") {
		t.Errorf("expected renamed topic in dashboard, got: %s", txt3)
	}

	// 4. Test delete topic
	_, err = sm.DeleteTopic(channelID, chatID, t2.ID)
	if err != nil {
		t.Fatalf("failed to delete topic: %v", err)
	}
	txt4, _, _ := topicUI.RenderTopicDashboard(channelID, chatID, userID)
	if strings.Contains(txt4, "OneDrive Analitik & Log") {
		t.Errorf("expected topic 2 to be deleted from dashboard, got: %s", txt4)
	}
}
