package storage

import (
	"path/filepath"
	"testing"
)

func TestChannelRecordGrapariOnly(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_grapari.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	ch := &ChannelRecord{
		ID:         "tg_grapari_test",
		Type:       "telegram",
		Name:       "GraPARI CS Bot",
		Identifier: "123456:BOT_TOKEN",
		IsActive:   true,
	}

	// Initially false
	if ch.IsGrapariOnly() {
		t.Errorf("expected IsGrapariOnly() to be false initially, got true")
	}

	// Set to true
	ch.SetGrapariOnly(true)
	if !ch.IsGrapariOnly() {
		t.Errorf("expected IsGrapariOnly() to be true after SetGrapariOnly(true)")
	}

	// Save to DB
	if err := db.SaveChannel(ch); err != nil {
		t.Fatalf("failed to save channel: %v", err)
	}

	// Reload from DB
	loaded, err := db.GetChannel("tg_grapari_test")
	if err != nil {
		t.Fatalf("failed to load channel: %v", err)
	}
	if loaded == nil {
		t.Fatalf("loaded channel is nil")
	}
	if !loaded.IsGrapariOnly() {
		t.Errorf("expected loaded channel to have IsGrapariOnly() == true")
	}

	// Toggle back to false
	loaded.SetGrapariOnly(false)
	if loaded.IsGrapariOnly() {
		t.Errorf("expected IsGrapariOnly() to be false after SetGrapariOnly(false)")
	}

	if err := db.SaveChannel(loaded); err != nil {
		t.Fatalf("failed to update channel: %v", err)
	}

	reloaded, _ := db.GetChannel("tg_grapari_test")
	if reloaded.IsGrapariOnly() {
		t.Errorf("expected reloaded channel to have IsGrapariOnly() == false")
	}
}
