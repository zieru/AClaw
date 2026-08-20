package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// MDLoader handles reading, writing, and caching Markdown files
type MDLoader struct {
	mu     sync.RWMutex
	dir    string
	cache  map[string]string
}

func NewMDLoader(dir string) *MDLoader {
	_ = os.MkdirAll(dir, 0755)
	return &MDLoader{
		dir:   dir,
		cache: make(map[string]string),
	}
}

// GetFile reads a markdown file by name (e.g. "IDENTITY.md")
func (m *MDLoader) GetFile(filename string) (string, error) {
	m.mu.RLock()
	if content, ok := m.cache[filename]; ok {
		m.mu.RUnlock()
		return content, nil
	}
	m.mu.RUnlock()

	filePath := filepath.Join(m.dir, filename)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("gagal membaca file %s: %w", filename, err)
	}

	content := string(data)
	m.mu.Lock()
	m.cache[filename] = content
	m.mu.Unlock()

	return content, nil
}

// SaveFile writes or updates a markdown file and invalidates cache
func (m *MDLoader) SaveFile(filename, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	filePath := filepath.Join(m.dir, filename)
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("gagal menyimpan file %s: %w", filename, err)
	}

	m.cache[filename] = content
	return nil
}

// ListFiles returns all .md files in directory
func (m *MDLoader) ListFiles() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".md" {
			files = append(files, e.Name())
		}
	}
	return files, nil
}

// Reload clears memory cache for hot reloading
func (m *MDLoader) Reload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache = make(map[string]string)
}
