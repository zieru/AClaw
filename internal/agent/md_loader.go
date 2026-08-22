package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// MDFileStatus represents the status of an MD file for a specific channel
type MDFileStatus struct {
	Filename    string
	IsCustom    bool // true if exists in channel directory
	CharCount   int
	SourcePath  string
	Inherited   bool // true if falling back to global
}

// MDLoader handles reading, writing, and caching Markdown files globally and per-channel
type MDLoader struct {
	mu    sync.RWMutex
	dir   string
	cache map[string]string
}

func NewMDLoader(dir string) *MDLoader {
	_ = os.MkdirAll(dir, 0755)
	_ = os.MkdirAll(filepath.Join(dir, "channels"), 0755)
	return &MDLoader{
		dir:   dir,
		cache: make(map[string]string),
	}
}

var safeChannelIDRegex = regexp.MustCompile(`[^a-zA-Z0-9_\-\.]`)

func sanitizeChannelID(channelID string) string {
	sanitized := safeChannelIDRegex.ReplaceAllString(channelID, "_")
	if sanitized == "" {
		return "default"
	}
	return sanitized
}

func (m *MDLoader) channelDir(channelID string) string {
	return filepath.Join(m.dir, "channels", sanitizeChannelID(channelID))
}

// GetFile reads a global markdown file by name (e.g. "IDENTITY.md")
func (m *MDLoader) GetFile(filename string) (string, error) {
	cacheKey := "global:" + filename
	m.mu.RLock()
	if content, ok := m.cache[cacheKey]; ok {
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
		return "", fmt.Errorf("gagal membaca file global %s: %w", filename, err)
	}

	content := string(data)
	m.mu.Lock()
	m.cache[cacheKey] = content
	m.mu.Unlock()

	return content, nil
}

// GetFileForChannel reads a markdown file for a channel. If not customized, falls back to global.
func (m *MDLoader) GetFileForChannel(channelID, filename string) (string, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || channelID == "global" {
		return m.GetFile(filename)
	}

	cacheKey := sanitizeChannelID(channelID) + ":" + filename
	m.mu.RLock()
	if content, ok := m.cache[cacheKey]; ok {
		m.mu.RUnlock()
		return content, nil
	}
	m.mu.RUnlock()

	// 1. Try Channel Specific Directory
	chFilePath := filepath.Join(m.channelDir(channelID), filename)
	data, err := os.ReadFile(chFilePath)
	if err == nil && len(strings.TrimSpace(string(data))) > 0 {
		content := string(data)
		m.mu.Lock()
		m.cache[cacheKey] = content
		m.mu.Unlock()
		return content, nil
	}

	// 2. Fallback to Global File
	globalContent, err := m.GetFile(filename)
	if err != nil {
		return "", err
	}

	// Cache fallback result under channel key
	m.mu.Lock()
	m.cache[cacheKey] = globalContent
	m.mu.Unlock()

	return globalContent, nil
}

// SaveFile writes or updates a global markdown file and invalidates cache
func (m *MDLoader) SaveFile(filename, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	filePath := filepath.Join(m.dir, filename)
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("gagal menyimpan file %s: %w", filename, err)
	}

	m.cache["global:"+filename] = content
	// Invalidate any channel fallback cache
	for k := range m.cache {
		if strings.HasSuffix(k, ":"+filename) {
			delete(m.cache, k)
		}
	}
	return nil
}

// SaveFileForChannel writes or updates a channel-specific markdown file
func (m *MDLoader) SaveFileForChannel(channelID, filename, content string) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || channelID == "global" {
		return m.SaveFile(filename, content)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dir := m.channelDir(channelID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("gagal membuat direktori channel %s: %w", channelID, err)
	}

	filePath := filepath.Join(dir, filename)
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("gagal menyimpan file channel %s/%s: %w", channelID, filename, err)
	}

	cacheKey := sanitizeChannelID(channelID) + ":" + filename
	m.cache[cacheKey] = content
	return nil
}

// DeleteFileForChannel removes a channel-specific override to revert to global default
func (m *MDLoader) DeleteFileForChannel(channelID, filename string) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || channelID == "global" {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	filePath := filepath.Join(m.channelDir(channelID), filename)
	_ = os.Remove(filePath)

	cacheKey := sanitizeChannelID(channelID) + ":" + filename
	delete(m.cache, cacheKey)
	return nil
}

// ListFiles returns all global .md files in directory
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
	sort.Strings(files)
	return files, nil
}

// ListFilesForChannel returns all .md files customized for a specific channel
func (m *MDLoader) ListFilesForChannel(channelID string) ([]string, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || channelID == "global" {
		return m.ListFiles()
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	dir := m.channelDir(channelID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".md" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

// GetChannelMDStatus returns comprehensive status of all standard and custom MD files for a channel
func (m *MDLoader) GetChannelMDStatus(channelID string) ([]MDFileStatus, error) {
	standardFiles := []string{"IDENTITY.md", "SOUL.md", "AGENTS.md", "TOOLS.md"}
	fileMap := make(map[string]bool)
	for _, f := range standardFiles {
		fileMap[f] = true
	}

	// Add any global files
	globalFiles, _ := m.ListFiles()
	for _, f := range globalFiles {
		fileMap[f] = true
	}

	// Add any channel files
	if channelID != "" && channelID != "global" {
		chFiles, _ := m.ListFilesForChannel(channelID)
		for _, f := range chFiles {
			fileMap[f] = true
		}
	}

	var allFilenames []string
	for f := range fileMap {
		allFilenames = append(allFilenames, f)
	}
	sort.Strings(allFilenames)

	var results []MDFileStatus
	for _, filename := range allFilenames {
		status := MDFileStatus{Filename: filename}
		if channelID == "" || channelID == "global" {
			content, _ := m.GetFile(filename)
			status.IsCustom = false
			status.Inherited = false
			status.CharCount = len(content)
			status.SourcePath = filepath.Join(m.dir, filename)
		} else {
			chPath := filepath.Join(m.channelDir(channelID), filename)
			if data, err := os.ReadFile(chPath); err == nil && len(strings.TrimSpace(string(data))) > 0 {
				status.IsCustom = true
				status.Inherited = false
				status.CharCount = len(string(data))
				status.SourcePath = chPath
			} else {
				content, _ := m.GetFile(filename)
				status.IsCustom = false
				status.Inherited = true
				status.CharCount = len(content)
				status.SourcePath = filepath.Join(m.dir, filename)
			}
		}
		results = append(results, status)
	}

	return results, nil
}

// Reload clears memory cache for hot reloading
func (m *MDLoader) Reload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache = make(map[string]string)
}
