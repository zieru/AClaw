package pypptx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureScriptsExtracted(t *testing.T) {
	tempDir := t.TempDir()
	targetDir := filepath.Join(tempDir, "extracted_pptx")

	err := EnsureScriptsExtracted(targetDir)
	if err != nil {
		t.Fatalf("EnsureScriptsExtracted failed: %v", err)
	}

	cliPath := filepath.Join(targetDir, "pptx_cli.py")
	if info, err := os.Stat(cliPath); err != nil || info.IsDir() {
		t.Fatalf("pptx_cli.py not found in extracted directory: %v", err)
	}

	reqPath := filepath.Join(targetDir, "requirements.txt")
	if info, err := os.Stat(reqPath); err != nil || info.IsDir() {
		t.Fatalf("requirements.txt not found in extracted directory: %v", err)
	}
}
