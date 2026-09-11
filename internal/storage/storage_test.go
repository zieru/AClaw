package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorageAndPolicyResolver(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// 1. Test Default Policy Resolution (Default global footer is now 'full')
	pol := db.GetResolvedPolicy("chan_tg", "chat_group_1")
	if pol.MaxUploadFileMB != 10 || pol.MaxTokens != 2048 || !pol.AutoCompaction || pol.FooterMode != "full" {
		t.Fatalf("unexpected default policy: %+v", pol)
	}

	// 2. Test Global Policy Overlay
	err = db.SavePolicy(&PolicyRecord{
		Scope:               "global",
		ScopeID:             "system",
		MaxUploadFileMB:     15,
		MaxTokens:           4096,
		MaxHistoryTurns:     25,
		AutoCompaction:      true,
		CompactionThreshold: 20,
		FooterMode:          "tokens",
	})
	if err != nil {
		t.Fatalf("failed to save global policy: %v", err)
	}

	pol = db.GetResolvedPolicy("chan_tg", "chat_group_1")
	if pol.MaxUploadFileMB != 15 || pol.MaxTokens != 4096 || pol.MaxHistoryTurns != 25 || pol.FooterMode != "tokens" {
		t.Fatalf("global policy did not overlay properly: %+v", pol)
	}

	// 3. Test Channel Policy Overlay
	err = db.SavePolicy(&PolicyRecord{
		Scope:           "channel",
		ScopeID:         "chan_tg",
		MaxUploadFileMB: 8,
		FooterMode:      "full",
	})
	if err != nil {
		t.Fatalf("failed to save channel policy: %v", err)
	}

	pol = db.GetResolvedPolicy("chan_tg", "chat_group_1")
	if pol.MaxUploadFileMB != 8 || pol.MaxTokens != 4096 || pol.FooterMode != "full" {
		t.Fatalf("channel policy did not overlay properly: %+v", pol)
	}

	// 4. Test Chat/Group Policy Override
	err = db.SavePolicy(&PolicyRecord{
		Scope:            "chat",
		ScopeID:          "chat_group_1",
		MaxUploadFileMB:  2,
		ModelOverride:    "gpt-4o-mini",
		FooterMode:       "off",
		StreamingEnabled: false,
	})
	if err != nil {
		t.Fatalf("failed to save chat policy: %v", err)
	}

	pol = db.GetResolvedPolicy("chan_tg", "chat_group_1")
	if pol.MaxUploadFileMB != 2 || pol.ModelOverride != "gpt-4o-mini" || pol.MaxTokens != 4096 || pol.FooterMode != "off" || pol.StreamingEnabled {
		t.Fatalf("chat policy did not override properly: %+v", pol)
	}

	// 5. Test Session & Truncation
	sess, err := db.GetOrCreateSession("chan_tg", "chat_group_1", "user_123")
	if err != nil {
		t.Fatalf("failed to get/create session: %v", err)
	}

	for i := 1; i <= 10; i++ {
		_ = db.AddMessage(sess.ID, "user", "test message", 10)
	}

	count, _ := db.CountSessionMessages(sess.ID)
	if count != 10 {
		t.Fatalf("expected 10 messages, got %d", count)
	}

	// Truncate to 4 messages
	err = db.TruncateOldMessages(sess.ID, 4)
	if err != nil {
		t.Fatalf("failed to truncate messages: %v", err)
	}

	countAfter, _ := db.CountSessionMessages(sess.ID)
	if countAfter != 4 {
		t.Fatalf("expected 4 messages after truncate, got %d", countAfter)
	}

	// 6. Test Audit Logging
	err = db.InsertAuditLog(&AuditLogRecord{
		ChannelType:  "telegram",
		ChannelID:    "chan_tg",
		ChatID:       "chat_group_1",
		UserID:       "user_123",
		UserName:     "testuser",
		Provider:     "9router",
		Model:        "gpt-4o-mini",
		PromptTokens: 100,
		TotalTokens:  150,
		CostUSD:      0.0001,
		Status:       "success",
	})
	if err != nil {
		t.Fatalf("failed to insert audit log: %v", err)
	}

	stats, err := db.GetStatsSummary(time.Now().Add(-1 * time.Hour))
	if err != nil {
		t.Fatalf("failed to get stats summary: %v", err)
	}
	if stats.TotalRequests != 1 || stats.TotalTokens != 150 {
		t.Fatalf("unexpected stats summary: %+v", stats)
	}

	// 7. Test Audit Log Rotation
	for i := 2; i <= 10; i++ {
		_ = db.InsertAuditLog(&AuditLogRecord{
			ChannelType: "telegram",
			ChannelID:   "chan_tg",
			ChatID:      "chat_group_1",
			UserID:      "user_123",
			UserName:    "testuser",
			Provider:    "9router",
			Model:       "gpt-4o-mini",
			TotalTokens: 10,
			Status:      "success",
		})
	}

	totalAudit, _ := db.CountAuditLogs()
	if totalAudit != 10 {
		t.Fatalf("expected 10 audit logs, got %d", totalAudit)
	}

	deleted, err := db.RotateAuditLogs(5)
	if err != nil {
		t.Fatalf("failed to rotate audit logs: %v", err)
	}
	if deleted != 5 {
		t.Fatalf("expected 5 deleted logs, got %d", deleted)
	}

	countAfterRot, _ := db.CountAuditLogs()
	if countAfterRot != 5 {
		t.Fatalf("expected 5 logs remaining after rotation, got %d", countAfterRot)
	}
}

func TestProviderModelToggle(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_toggle.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	prov := &ProviderRecord{
		ID:           "test-prov",
		Name:         "Test Provider",
		Type:         "openai",
		DefaultModel: "gpt-4o-mini",
		Models:       []string{"gpt-4o-mini", "gpt-4o", "o3-mini", "claude-3-5-sonnet"},
		IsActive:     true,
	}

	// 1. Initially, no disabled models, all models should be enabled
	if len(prov.EnabledModels()) != 4 {
		t.Fatalf("expected 4 enabled models, got %v", prov.EnabledModels())
	}
	if !prov.IsModelEnabled("gpt-4o") {
		t.Fatalf("expected gpt-4o to be enabled initially")
	}

	// 2. Toggle gpt-4o -> disabled
	enabled := prov.ToggleModel("gpt-4o")
	if enabled {
		t.Fatalf("expected gpt-4o to become disabled")
	}
	if prov.IsModelEnabled("gpt-4o") {
		t.Fatalf("expected gpt-4o to be disabled")
	}
	if len(prov.EnabledModels()) != 3 {
		t.Fatalf("expected 3 enabled models, got %v", prov.EnabledModels())
	}

	// 3. Save to database
	if err := db.SaveProvider(prov); err != nil {
		t.Fatalf("failed to save provider: %v", err)
	}

	// 4. Retrieve from database
	loaded, err := db.GetProvider("test-prov")
	if err != nil || loaded == nil {
		t.Fatalf("failed to load provider: %v", err)
	}
	if loaded.IsModelEnabled("gpt-4o") {
		t.Fatalf("expected gpt-4o to remain disabled after db load")
	}
	if !loaded.IsModelEnabled("gpt-4o-mini") {
		t.Fatalf("expected gpt-4o-mini to be enabled")
	}
	if len(loaded.EnabledModels()) != 3 {
		t.Fatalf("expected 3 enabled models from db, got %v", loaded.EnabledModels())
	}

	// 5. Toggle gpt-4o back on
	enabled = loaded.ToggleModel("gpt-4o")
	if !enabled {
		t.Fatalf("expected gpt-4o to become enabled")
	}
	if !loaded.IsModelEnabled("gpt-4o") {
		t.Fatalf("expected gpt-4o to be enabled now")
	}

	// 6. Test SetAllModelsState(false)
	loaded.SetAllModelsState(false)
	// should disable all except defaultModel
	enabledList := loaded.EnabledModels()
	if len(enabledList) != 1 || enabledList[0] != "gpt-4o-mini" {
		t.Fatalf("expected only default model enabled, got %v", enabledList)
	}

	// 7. Test SetAllModelsState(true)
	loaded.SetAllModelsState(true)
	if len(loaded.EnabledModels()) != 4 {
		t.Fatalf("expected all 4 models enabled, got %v", loaded.EnabledModels())
	}
}

func TestMultiTopicChatSessions(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_topics.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	channelID := "chan_tg"
	chatID := "chat_123"
	userID := "user_456"

	// 1. Initial GetOrCreateSession creates default active session
	s1, err := db.GetOrCreateSession(channelID, chatID, userID)
	if err != nil {
		t.Fatalf("failed to create initial session: %v", err)
	}
	if !s1.IsActive || s1.Title != "Topik Utama" {
		t.Fatalf("unexpected initial session: %+v", s1)
	}

	_ = db.AddMessage(s1.ID, "user", "pesan topik 1", 5)

	// 2. Create Topic 2 (setActive = true)
	s2, err := db.CreateChatSession(channelID, chatID, userID, "Analisis Server", true)
	if err != nil {
		t.Fatalf("failed to create topic 2: %v", err)
	}
	if !s2.IsActive || s2.Title != "Analisis Server" {
		t.Fatalf("unexpected topic 2: %+v", s2)
	}
	_ = db.AddMessage(s2.ID, "user", "pesan topik 2", 10)

	// Verify s1 is now inactive and s2 is active
	activeSess, err := db.GetOrCreateSession(channelID, chatID, userID)
	if err != nil || activeSess.ID != s2.ID {
		t.Fatalf("expected s2 to be active, got: %+v", activeSess)
	}

	// 3. ListChatSessions
	topics, err := db.ListChatSessions(channelID, chatID)
	if err != nil {
		t.Fatalf("failed to list sessions: %v", err)
	}
	if len(topics) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(topics))
	}

	// 4. Verify message isolation
	msgsS1, _ := db.GetRecentMessages(s1.ID, 10)
	msgsS2, _ := db.GetRecentMessages(s2.ID, 10)
	if len(msgsS1) != 1 || msgsS1[0].Content != "pesan topik 1" {
		t.Fatalf("topic 1 messages corrupted: %+v", msgsS1)
	}
	if len(msgsS2) != 1 || msgsS2[0].Content != "pesan topik 2" {
		t.Fatalf("topic 2 messages corrupted: %+v", msgsS2)
	}

	// 5. Switch back to Topic 1
	switched, err := db.SwitchChatSession(channelID, chatID, s1.ID)
	if err != nil || !switched.IsActive || switched.ID != s1.ID {
		t.Fatalf("failed to switch to s1: %v, %+v", err, switched)
	}
	activeNow, _ := db.GetOrCreateSession(channelID, chatID, userID)
	if activeNow.ID != s1.ID {
		t.Fatalf("expected s1 to be active now, got: %s", activeNow.ID)
	}

	// 6. Rename Topic
	err = db.RenameChatSession(s2.ID, "Investigasi Error")
	if err != nil {
		t.Fatalf("failed to rename topic: %v", err)
	}
	renamed, _ := db.GetSessionByID(s2.ID)
	if renamed.Title != "Investigasi Error" {
		t.Fatalf("expected renamed title 'Investigasi Error', got '%s'", renamed.Title)
	}

	// 7. ClearActiveSessionMessages resets only active session (s1)
	err = db.ClearActiveSessionMessages(channelID, chatID)
	if err != nil {
		t.Fatalf("failed to clear active messages: %v", err)
	}
	msgsS1After, _ := db.GetRecentMessages(s1.ID, 10)
	msgsS2After, _ := db.GetRecentMessages(s2.ID, 10)
	if len(msgsS1After) != 0 {
		t.Fatalf("expected s1 messages cleared, got %d", len(msgsS1After))
	}
	if len(msgsS2After) != 1 {
		t.Fatalf("s2 messages should remain untouched, got %d", len(msgsS2After))
	}

	// 8. Delete active session (s1), should fallback to s2 as active
	nextActive, err := db.DeleteChatSession(channelID, chatID, s1.ID)
	if err != nil || nextActive == nil || nextActive.ID != s2.ID || !nextActive.IsActive {
		t.Fatalf("expected fallback to s2 after deleting s1, got: %v, %+v", err, nextActive)
	}
}

func TestLegacyMigration_NoColumnIsActive(t *testing.T) {
	tempDir := t.TempDir()
	legacyDbPath := filepath.Join(tempDir, "legacy_no_is_active.db")

	// 1. Manually create a legacy SQLite database with the old schema (without is_active, with UNIQUE(channel_id, chat_id))
	rawDB, err := sql.Open("sqlite", legacyDbPath)
	if err != nil {
		t.Fatalf("failed to open raw db: %v", err)
	}

	legacySchema := `
	CREATE TABLE chat_sessions (
		id TEXT PRIMARY KEY,
		channel_id TEXT NOT NULL,
		chat_id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(channel_id, chat_id)
	);
	CREATE TABLE chat_messages (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		tokens INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	INSERT INTO chat_sessions (id, channel_id, chat_id, user_id, title, summary)
	VALUES ('sess_old_1', 'telegram', 'chat_old_999', 'user_1', 'Old Session', 'Summary old');
	INSERT INTO chat_messages (id, session_id, role, content, tokens)
	VALUES ('msg_old_1', 'sess_old_1', 'user', 'hello legacy world', 10);
	`
	if _, err := rawDB.Exec(legacySchema); err != nil {
		t.Fatalf("failed to populate legacy db: %v", err)
	}
	_ = rawDB.Close()

	// 2. Now open with storage.Open which must run migrations cleanly without crashing
	migratedDB, err := Open(legacyDbPath)
	if err != nil {
		t.Fatalf("Open failed on legacy database: %v", err)
	}
	defer migratedDB.Close()

	// 3. Verify old session still exists and has is_active = 1
	sess, err := migratedDB.GetSessionByID("sess_old_1")
	if err != nil || sess == nil {
		t.Fatalf("expected to find old session, got err: %v", err)
	}
	if sess.Title != "Old Session" || !sess.IsActive {
		t.Fatalf("expected old session intact and active, got: %+v", sess)
	}

	// 4. Verify old message is intact
	msgs, err := migratedDB.GetRecentMessages("sess_old_1", 10)
	if err != nil || len(msgs) != 1 || msgs[0].Content != "hello legacy world" {
		t.Fatalf("expected old message intact, got: %v, %+v", err, msgs)
	}

	// 5. Verify we can now add a second session in the same (channel_id, chat_id) without UNIQUE constraint violation
	s2, err := migratedDB.CreateChatSession("telegram", "chat_old_999", "user_1", "Second Session", true)
	if err != nil {
		t.Fatalf("failed to create second session on migrated db: %v", err)
	}
	if !s2.IsActive {
		t.Fatalf("expected s2 to be active")
	}

	// 6. Verify listing sessions returns both
	allSessions, err := migratedDB.ListChatSessions("telegram", "chat_old_999")
	if err != nil || len(allSessions) != 2 {
		t.Fatalf("expected 2 sessions, got: %d (err: %v)", len(allSessions), err)
	}
}

func TestOpenActualDataDBIfExists(t *testing.T) {
	dbPath := filepath.Join("..", "..", "data", "goassistant.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skip("data/goassistant.db not present, skipping")
	}
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed on real data/goassistant.db: %v", err)
	}
	defer db.Close()

	// Verify we can query chat_sessions
	sessions, err := db.ListChatSessions("system", "default")
	if err != nil {
		t.Fatalf("failed to query chat_sessions: %v", err)
	}
	_ = sessions
}



