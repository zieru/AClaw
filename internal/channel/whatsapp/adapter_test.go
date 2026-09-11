package whatsapp

import (
	"path/filepath"
	"testing"

	"goassistant/internal/storage"
)

func TestWhatsApp_TopicCommands(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_wa_topic.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	adapter := &NativeAdapter{
		channelID: "wa-test",
		name:      "Test WA",
		db:        db,
	}

	chatID := "628123456789@s.whatsapp.net"
	senderID := "628123456789"

	// 1. Initial /topic
	handled := adapter.handleTopicCommand(chatID, senderID, "/topic", "/topic")
	if !handled {
		t.Errorf("expected /topic to be handled")
	}

	sessions, err := db.ListChatSessions("wa-test", chatID)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("expected 1 initial session, got %d (err: %v)", len(sessions), err)
	}
	if sessions[0].Title != "Topik Utama" {
		t.Errorf("expected initial title 'Topik Utama', got '%s'", sessions[0].Title)
	}

	// 2. /newtopic Proyek Bot AI
	handled = adapter.handleTopicCommand(chatID, senderID, "/newtopic Proyek Bot AI", "/newtopic proyek bot ai")
	if !handled {
		t.Errorf("expected /newtopic to be handled")
	}

	sessions, _ = db.ListChatSessions("wa-test", chatID)
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions after /newtopic, got %d", len(sessions))
	}

	active, err := db.GetOrCreateSession("wa-test", chatID, senderID)
	if err != nil {
		t.Fatalf("failed to get active session: %v", err)
	}
	if active.Title != "Proyek Bot AI" {
		t.Errorf("expected newly created topic to be active with title 'Proyek Bot AI', got '%s'", active.Title)
	}

	// 3. /renametopic Proyek GoAssistant
	handled = adapter.handleTopicCommand(chatID, senderID, "/renametopic Proyek GoAssistant", "/renametopic proyek goassistant")
	if !handled {
		t.Errorf("expected /renametopic to be handled")
	}

	active, _ = db.GetOrCreateSession("wa-test", chatID, senderID)
	if active.Title != "Proyek GoAssistant" {
		t.Errorf("expected renamed title 'Proyek GoAssistant', got '%s'", active.Title)
	}

	// 4. /switchtopic 2 (switch to the second session: Topik Utama)
	handled = adapter.handleTopicCommand(chatID, senderID, "/switchtopic 2", "/switchtopic 2")
	if !handled {
		t.Errorf("expected /switchtopic to be handled")
	}

	active, _ = db.GetOrCreateSession("wa-test", chatID, senderID)
	if active.Title != "Topik Utama" {
		t.Errorf("expected switched active session to be 'Topik Utama', got '%s'", active.Title)
	}

	// 5. /deltopic 2 (delete the second session: Proyek GoAssistant)
	handled = adapter.handleTopicCommand(chatID, senderID, "/deltopic 2", "/deltopic 2")
	if !handled {
		t.Errorf("expected /deltopic to be handled")
	}

	sessions, _ = db.ListChatSessions("wa-test", chatID)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session after deletion, got %d", len(sessions))
	}
	if sessions[0].Title != "Topik Utama" {
		t.Errorf("expected remaining session to be 'Topik Utama', got '%s'", sessions[0].Title)
	}

	// 6. Non-topic command
	handled = adapter.handleTopicCommand(chatID, senderID, "Halo bot", "halo bot")
	if handled {
		t.Errorf("expected normal message not to be handled as topic command")
	}
}
