package webadmin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/storage"
)

func TestServer_StartRestartStop(t *testing.T) {
	cfg := &config.AppConfig{}
	cfg.WebAdmin.Port = 12981
	cfg.AdminTelegram.AllowedUserIDs = []int64{123}

	// Create test SQLite DB in memory or temp
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open memory db: %v", err)
	}
	defer db.Close()

	srv := NewServer(cfg, db, nil, nil)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Test accessing port 12981
	resp, err := http.Get("http://localhost:12981/api/auth/me")
	if err != nil {
		t.Fatalf("failed to query server on port 12981: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}

	// Test dynamic rebind / Restart to port 12982
	if err := srv.Restart(12982); err != nil {
		t.Fatalf("failed to restart server to port 12982: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Old port should no longer accept connections
	_, oldErr := http.Get("http://localhost:12981/api/auth/me")
	if oldErr == nil {
		t.Fatalf("expected old port 12981 to be closed")
	}

	// New port should work
	newResp, err := http.Get("http://localhost:12982/api/auth/me")
	if err != nil {
		t.Fatalf("failed to query server on new port 12982: %v", err)
	}
	newResp.Body.Close()
	if newResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on new port, got %d", newResp.StatusCode)
	}

	// Verify DB saved setting
	savedPort, err := db.GetSetting("webadmin_port", "")
	if err != nil || savedPort != "12982" {
		t.Fatalf("expected saved port 12982 in db, got '%s'", savedPort)
	}

	// Stop server
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}
}

func TestServer_ServeUI(t *testing.T) {
	cfg := &config.AppConfig{}
	cfg.WebAdmin.Port = 12983

	srv := NewServer(cfg, nil, nil, nil)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() {
		_ = srv.Stop(context.Background())
	}()

	time.Sleep(100 * time.Millisecond)

	// Access /admin/
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/admin/", cfg.WebAdmin.Port))
	if err != nil {
		t.Fatalf("failed to fetch /admin/: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyStr := string(bodyBytes)
	if len(bodyStr) == 0 {
		t.Fatalf("expected non-empty HTML body")
	}
}
