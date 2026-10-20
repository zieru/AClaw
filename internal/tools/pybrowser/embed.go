package pybrowser

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

//go:embed server.py pyproject.toml setup.sh setup.bat
var ScriptFS embed.FS

// EnsureScriptsExtracted extracts embedded scripts to targetDir if they are missing.
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
			perm := os.FileMode(0644)
			if filepath.Ext(entry.Name()) == ".sh" || filepath.Ext(entry.Name()) == ".bat" {
				perm = 0755
			}
			if err := os.WriteFile(targetFile, data, perm); err != nil {
				return fmt.Errorf("failed to write %s: %w", targetFile, err)
			}
			extractedCount++
		}
	}

	if extractedCount > 0 {
		log.Printf("[pybrowser] Auto-extracted %d MCP script files to %s", extractedCount, targetDir)
	}
	return nil
}
