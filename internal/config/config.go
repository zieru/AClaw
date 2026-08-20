package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// AppConfig represents global static configuration
type AppConfig struct {
	mu sync.RWMutex

	Server struct {
		DataDir   string `yaml:"data_dir"`
		DBPath    string `yaml:"db_path"`
		MDDir     string `yaml:"md_dir"`
		LogDir    string `yaml:"log_dir"`
		LogLevel  string `yaml:"log_level"`
	} `yaml:"server"`

	AdminTelegram struct {
		BotToken       string   `yaml:"bot_token"`
		AllowedUserIDs []int64  `yaml:"allowed_user_ids"`
		PollTimeout    int      `yaml:"poll_timeout"`
	} `yaml:"admin_telegram"`

	Defaults struct {
		DefaultProvider string  `yaml:"default_provider"`
		DefaultModel    string  `yaml:"default_model"`
		Temperature     float64 `yaml:"temperature"`
		MaxTokens       int     `yaml:"max_tokens"`
		MaxContextTurns int     `yaml:"max_context_turns"`
		TokenBudget     int     `yaml:"token_budget"`
	} `yaml:"defaults"`
}

var (
	globalConfig *AppConfig
	configOnce   sync.Once
)

// Get returns the loaded configuration singleton
func Get() *AppConfig {
	return globalConfig
}

// Load reads and parses the YAML configuration file
func Load(configPath string) (*AppConfig, error) {
	var err error
	configOnce.Do(func() {
		cfg := &AppConfig{}
		// Set defaults
		cfg.Server.DataDir = "./data"
		cfg.Server.DBPath = "./data/goassistant.db"
		cfg.Server.MDDir = "./data/md"
		cfg.Server.LogDir = "./data/logs"
		cfg.Server.LogLevel = "info"
		cfg.AdminTelegram.PollTimeout = 30
		cfg.Defaults.DefaultProvider = "gemini"
		cfg.Defaults.DefaultModel = "gemini-2.0-flash"
		cfg.Defaults.Temperature = 0.7
		cfg.Defaults.MaxTokens = 2048
		cfg.Defaults.MaxContextTurns = 20
		cfg.Defaults.TokenBudget = 8000

		if configPath != "" {
			if _, statErr := os.Stat(configPath); statErr != nil {
				err = fmt.Errorf("file config %s tidak ditemukan: %w", configPath, statErr)
				return
			}
			data, readErr := os.ReadFile(configPath)
			if readErr != nil {
				err = fmt.Errorf("gagal membaca file config %s: %w", configPath, readErr)
				return
			}
			if yamlErr := yaml.Unmarshal(data, cfg); yamlErr != nil {
				err = fmt.Errorf("gagal parsing yaml %s: %w", configPath, yamlErr)
				return
			}
		}

		// Ensure directories exist
		_ = os.MkdirAll(cfg.Server.DataDir, 0755)
		_ = os.MkdirAll(cfg.Server.MDDir, 0755)
		_ = os.MkdirAll(cfg.Server.LogDir, 0755)
		_ = os.MkdirAll(filepath.Dir(cfg.Server.DBPath), 0755)

		globalConfig = cfg
	})

	return globalConfig, err
}

// IsAdminUser checks if given Telegram User ID is authorized
func (c *AppConfig) IsAdminUser(userID int64) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.AdminTelegram.AllowedUserIDs) == 0 {
		return true // If empty, allow for initial bootstrap
	}

	for _, id := range c.AdminTelegram.AllowedUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// AddAdminUser adds a user ID to allowed admin list dynamically
func (c *AppConfig) AddAdminUser(userID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, id := range c.AdminTelegram.AllowedUserIDs {
		if id == userID {
			return
		}
	}
	c.AdminTelegram.AllowedUserIDs = append(c.AdminTelegram.AllowedUserIDs, userID)
}
