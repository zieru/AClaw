package admin

import (
	"testing"

	"goassistant/internal/config"
	"goassistant/internal/storage"
	"goassistant/internal/webadmin"
)

func TestWebAdminUI_PortValidation(t *testing.T) {
	cfg := &config.AppConfig{}
	cfg.WebAdmin.Port = 12990

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	srv := webadmin.NewServer(cfg, db, nil, nil)
	ui := NewWebAdminUI(srv, db, nil)

	// Valid port check
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start webadmin server: %v", err)
	}
	defer func() {
		_ = srv.Stop(nil)
	}()

	if srv.GetPort() != 12990 {
		t.Fatalf("expected port 12990, got %d", srv.GetPort())
	}

	// Dynamic restart
	if err := srv.RestartPort(12991); err != nil {
		t.Fatalf("failed to restart port to 12991: %v", err)
	}

	if srv.GetPort() != 12991 {
		t.Fatalf("expected port 12991, got %d", srv.GetPort())
	}

	// Verify UI has server instance
	if ui.server == nil {
		t.Fatalf("expected ui.server to be non-nil")
	}
}
