package pypptx

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

//go:embed pptx_cli.py requirements.txt
var ScriptFS embed.FS

// EnsureScriptsExtracted mengekstrak script ter-embed ke targetDir jika belum ada atau berbeda
func EnsureScriptsExtracted(targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", targetDir, err)
	}

	entries, err := ScriptFS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("failed to read embedded script dir: %w", err)
	}

	extractedCount := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		targetFile := filepath.Join(targetDir, entry.Name())
		data, err := ScriptFS.ReadFile(entry.Name())
		if err != nil {
			return fmt.Errorf("failed to read embedded %s: %w", entry.Name(), err)
		}

		shouldWrite := false
		existingData, readErr := os.ReadFile(targetFile)
		if readErr != nil || string(existingData) != string(data) {
			shouldWrite = true
		}

		if shouldWrite {
			if err := os.WriteFile(targetFile, data, 0755); err != nil {
				return fmt.Errorf("failed to write %s: %w", targetFile, err)
			}
			extractedCount++
		}
	}

	if extractedCount > 0 {
		log.Printf("[pypptx] Auto-extracted %d PPTX engine script files to %s", extractedCount, targetDir)
	}
	return nil
}
