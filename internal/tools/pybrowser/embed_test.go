package pybrowser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureScriptsExtracted(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pybrowser_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	err = EnsureScriptsExtracted(tempDir)
	if err != nil {
		t.Fatalf("EnsureScriptsExtracted failed: %v", err)
	}

	files := []string{"server.py", "pyproject.toml", "setup.sh", "setup.bat"}
	for _, f := range files {
		target := filepath.Join(tempDir, f)
		if fi, err := os.Stat(target); os.IsNotExist(err) || fi.Size() == 0 {
			t.Errorf("expected extracted file %s to exist and not be empty", f)
		}
	}
}
