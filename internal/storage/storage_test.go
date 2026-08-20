package storage

import (
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

	// 1. Test Default Policy Resolution
	pol := db.GetResolvedPolicy("chan_tg", "chat_group_1")
	if pol.MaxUploadFileMB != 10 || pol.MaxTokens != 2048 || !pol.AutoCompaction || pol.FooterMode != "off" {
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
		Scope:           "chat",
		ScopeID:         "chat_group_1",
		MaxUploadFileMB: 2,
		ModelOverride:   "gpt-4o-mini",
		FooterMode:      "off",
	})
	if err != nil {
		t.Fatalf("failed to save chat policy: %v", err)
	}

	pol = db.GetResolvedPolicy("chan_tg", "chat_group_1")
	if pol.MaxUploadFileMB != 2 || pol.ModelOverride != "gpt-4o-mini" || pol.MaxTokens != 4096 || pol.FooterMode != "off" {
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
}
