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
