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

	ProxyPool struct {
		Enabled        bool     `yaml:"enabled"`
		Strategy       string   `yaml:"strategy"`
		InitialProxies []string `yaml:"initial_proxies"`
	} `yaml:"proxy_pool"`

	TokenSaver struct {
		DefaultMode string `yaml:"default_mode"` // off, auto, aggressive, caveman
	} `yaml:"token_saver"`

	Streaming struct {
		Enabled         bool   `yaml:"enabled"`          // Enable/disable streaming globally
		ThinkingEnabled bool   `yaml:"thinking_enabled"` // Show thinking/reasoning process
		ThinkingDisplay string `yaml:"thinking_display"` // full, summary, hidden
		ChunkDelayMs    int    `yaml:"chunk_delay_ms"`   // Delay between streaming chunks to Telegram
	} `yaml:"streaming"`

	Timeouts struct {
		APICallSeconds int `yaml:"api_call_seconds"`
		HandlerSeconds int `yaml:"handler_seconds"`
		RetrySeconds   int `yaml:"retry_seconds"`
	} `yaml:"timeouts"`

	SubAgent struct {
		MaxParallel        int  `yaml:"max_parallel"`          // Max concurrent sub-agents
		TimeoutSeconds     int  `yaml:"timeout_seconds"`       // Per-task timeout
		AutoDelegate       bool `yaml:"auto_delegate"`         // Auto-split complex prompts
		TokenBudgetPerTask int  `yaml:"token_budget_per_task"` // Max tokens per sub-agent task
	} `yaml:"subagent"`

	HTTPServer struct {
		Enabled             bool   `yaml:"enabled"`
		Port                int    `yaml:"port"`
		EndpointsFile       string `yaml:"endpoints_file"`
		ReadTimeoutSeconds  int    `yaml:"read_timeout_seconds"`
		WriteTimeoutSeconds int    `yaml:"write_timeout_seconds"`
	} `yaml:"http_server"`

	Updater struct {
		GitHubRepo string `yaml:"github_repo"`
	} `yaml:"updater"`
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
		cfg.ProxyPool.Enabled = true
		cfg.ProxyPool.Strategy = "round-robin"
		cfg.TokenSaver.DefaultMode = "auto"
		cfg.Streaming.Enabled = true
		cfg.Streaming.ThinkingEnabled = true
		cfg.Streaming.ThinkingDisplay = "full"
		cfg.Streaming.ChunkDelayMs = 500
		cfg.SubAgent.MaxParallel = 3
		cfg.SubAgent.TimeoutSeconds = 90
		cfg.SubAgent.AutoDelegate = true
		cfg.SubAgent.TokenBudgetPerTask = 2048
		cfg.HTTPServer.Enabled = true
		cfg.HTTPServer.Port = 8080
		cfg.HTTPServer.EndpointsFile = "configs/endpoints.yaml"
		cfg.HTTPServer.ReadTimeoutSeconds = 15
		cfg.HTTPServer.WriteTimeoutSeconds = 45

		cfg.Timeouts.APICallSeconds = 90
		cfg.Timeouts.HandlerSeconds = 120
		cfg.Timeouts.RetrySeconds = 120

		cfg.Updater.GitHubRepo = "zieru/AClaw"


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
