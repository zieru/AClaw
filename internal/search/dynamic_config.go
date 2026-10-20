package search

import (
	"encoding/json"
	"fmt"
	"strings"

	"goassistant/internal/config"
	"goassistant/internal/storage"
)

const SettingSearchConfigKey = "search_config"

// LoadDynamicConfig reads search configuration from SQLite system_settings table,
// falling back to fallback values if not found or corrupted.
func LoadDynamicConfig(db *storage.DB, fallback config.SearchConfig) config.SearchConfig {
	if db == nil {
		return fallback
	}

	savedJSON, err := db.GetSetting(SettingSearchConfigKey, "")
	if err != nil || strings.TrimSpace(savedJSON) == "" {
		return fallback
	}

	var savedCfg config.SearchConfig
	if err := json.Unmarshal([]byte(savedJSON), &savedCfg); err != nil {
		return fallback
	}

	// Preserve non-empty values from saved config while falling back to defaults for missing fields
	if savedCfg.Provider == "" {
		savedCfg.Provider = fallback.Provider
	}
	if savedCfg.Strategy == "" {
		savedCfg.Strategy = fallback.Strategy
	}
	if savedCfg.MaxResults <= 0 {
		savedCfg.MaxResults = fallback.MaxResults
	}
	if savedCfg.TimeoutSeconds <= 0 {
		savedCfg.TimeoutSeconds = fallback.TimeoutSeconds
	}
	if savedCfg.Tavily.BaseURL == "" {
		savedCfg.Tavily.BaseURL = fallback.Tavily.BaseURL
	}
	if savedCfg.Tavily.SearchDepth == "" {
		savedCfg.Tavily.SearchDepth = fallback.Tavily.SearchDepth
	}
	if savedCfg.Firecrawl.BaseURL == "" {
		savedCfg.Firecrawl.BaseURL = fallback.Firecrawl.BaseURL
	}

	return savedCfg
}

// SaveDynamicConfig writes search configuration JSON to SQLite system_settings table.
func SaveDynamicConfig(db *storage.DB, cfg config.SearchConfig) error {
	if db == nil {
		return fmt.Errorf("database nil")
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("gagal serialize search config: %w", err)
	}
	return db.SetSetting(SettingSearchConfigKey, string(data))
}
