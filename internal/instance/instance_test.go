package instance

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEnsureSingleInstance(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "goassistant_instance_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cleanup1, err := EnsureSingleInstance(tempDir)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	pidFile := filepath.Join(tempDir, "goassistant.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("failed to read pid file: %v", err)
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("expected pid %d, got %s", os.Getpid(), string(data))
	}

	// Calling EnsureSingleInstance again from the same process should succeed
	cleanup2, err := EnsureSingleInstance(tempDir)
	if err != nil {
		t.Fatalf("second acquire failed: %v", err)
	}

	cleanup2()
	cleanup1()

	// PID file should be cleaned up
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("pid file should have been removed after cleanup")
	}
}
