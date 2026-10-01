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

	WebAdmin struct {
		Enabled           bool   `yaml:"enabled"`
		BindAddress       string `yaml:"bind_address"`
		Port              int    `yaml:"port"`
		SessionTTLMinutes int    `yaml:"session_ttl_minutes"`
		OTPTTLMinutes     int    `yaml:"otp_ttl_minutes"`
		APIKey            string `yaml:"api_key"`
	} `yaml:"web_admin"`

	Updater struct {
		GitHubRepo string `yaml:"github_repo"`
	} `yaml:"updater"`

	Webshare struct {
		APIKey              string   `yaml:"api_key"`
		Mode                string   `yaml:"mode"` // direct or backbone
		Protocol            string   `yaml:"protocol"` // http or socks5
		Countries           []string `yaml:"countries"`
		AutoSync            bool     `yaml:"auto_sync"`
		SyncIntervalMinutes int      `yaml:"sync_interval_minutes"`
		GroupName           string   `yaml:"group_name"`
	} `yaml:"webshare"`

	Search SearchConfig `yaml:"search"`

	OmniRoute OmniRouteConfig `yaml:"omniroute"`

	Memory MemoryConfig `yaml:"memory"`

	MCPServers []MCPServerConfig `yaml:"mcp_servers"`
}

// MemoryConfig defines configuration for GoAssistant's standalone memory engine
type MemoryConfig struct {
	Enabled             bool            `yaml:"enabled"`
	AutoExtract         bool            `yaml:"auto_extract"`
	RetrievalStrategy   string          `yaml:"retrieval_strategy"` // "hybrid", "semantic", "exact"
	MaxContextItems     int             `yaml:"max_context_items"`
	SimilarityThreshold float64         `yaml:"similarity_threshold"`
	SeedCSVPath         string          `yaml:"seed_csv_path"`
	Embedding           EmbeddingConfig `yaml:"embedding"`
}

// EmbeddingConfig defines configuration for generating vector embeddings
type EmbeddingConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Provider   string `yaml:"provider"` // "openai", "gemini", "custom", "ollama", "omniroute"
	Model      string `yaml:"model"`
	BaseURL    string `yaml:"base_url"`
	APIKey     string `yaml:"api_key"`
	Dimensions int    `yaml:"dimensions"`
}

// SearchConfig defines configuration for GoAssistant's multi-provider search engine
type SearchConfig struct {
	Enabled         bool            `yaml:"enabled"`
	Provider        string          `yaml:"provider"` // "auto", "tavily", "firecrawl", "duckduckgo"
	Strategy        string          `yaml:"strategy"` // "fallback", "roundrobin"
	MaxResults      int             `yaml:"max_results"`
	FallbackEnabled bool            `yaml:"fallback_enabled"`
	TimeoutSeconds  int             `yaml:"timeout_seconds"`
	Tavily          TavilyConfig    `yaml:"tavily"`
	Firecrawl       FirecrawlConfig `yaml:"firecrawl"`
}

// TavilyConfig defines configuration for Tavily Search API
type TavilyConfig struct {
	APIKey        string `yaml:"api_key"`
	BaseURL       string `yaml:"base_url"`
	SearchDepth   string `yaml:"search_depth"`   // "basic" or "advanced"
	IncludeAnswer bool   `yaml:"include_answer"` // include quick AI answer
}

// FirecrawlConfig defines configuration for Firecrawl Search API
type FirecrawlConfig struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
}

// OmniRouteConfig defines configuration for co-located OmniRoute gateway collaboration
type OmniRouteConfig struct {
	Enabled           bool   `yaml:"enabled"`
	BaseURL           string `yaml:"base_url"`
	Password          string `yaml:"password"`
	APIKey            string `yaml:"api_key"`
	UseUpstreamSearch bool   `yaml:"use_upstream_search"`
	UseUpstreamMemory bool   `yaml:"use_upstream_memory"`
}

// MCPServerConfig defines an external Model Context Protocol (MCP) server
type MCPServerConfig struct {
	Name      string            `yaml:"name"`
	Enabled   bool              `yaml:"enabled"`
	Transport string            `yaml:"transport"` // "stdio" or "sse"
	Command   string            `yaml:"command"`   // Path or command for stdio
	Args      []string          `yaml:"args"`      // Arguments for stdio
	Env       map[string]string `yaml:"env"`       // Extra environment variables
	URL       string            `yaml:"url"`       // URL for SSE transport
	Prefix    string            `yaml:"prefix"`    // Tool name prefix (e.g. "a7g3")
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

		cfg.WebAdmin.Enabled = true
		cfg.WebAdmin.BindAddress = "0.0.0.0"
		cfg.WebAdmin.Port = 12111
		cfg.WebAdmin.SessionTTLMinutes = 1440
		cfg.WebAdmin.OTPTTLMinutes = 5

		cfg.Timeouts.APICallSeconds = 90
		cfg.Timeouts.HandlerSeconds = 120
		cfg.Timeouts.RetrySeconds = 120

		cfg.Updater.GitHubRepo = "zieru/AClaw"

		cfg.Webshare.Mode = "direct"
		cfg.Webshare.Protocol = "http"
		cfg.Webshare.GroupName = "webshare"
		cfg.Webshare.SyncIntervalMinutes = 60
		cfg.Webshare.AutoSync = false
 
		cfg.Search.Enabled = true
		cfg.Search.Provider = "auto"
		cfg.Search.Strategy = "fallback"
		cfg.Search.MaxResults = 5
		cfg.Search.FallbackEnabled = true
		cfg.Search.TimeoutSeconds = 15
		cfg.Search.Tavily.BaseURL = "https://api.tavily.com"
		cfg.Search.Tavily.SearchDepth = "basic"
		cfg.Search.Tavily.IncludeAnswer = true
		cfg.Search.Firecrawl.BaseURL = "https://api.firecrawl.dev"

		cfg.OmniRoute.Enabled = true
		cfg.OmniRoute.BaseURL = "http://localhost:20128"
		cfg.OmniRoute.Password = ""
		cfg.OmniRoute.UseUpstreamSearch = false // Severed: Web Search is now standalone multi-provider in GoAssistant
		cfg.OmniRoute.UseUpstreamMemory = false // Severed: Standalone memory in GoAssistant

		cfg.Memory.Enabled = true
		cfg.Memory.AutoExtract = true
		cfg.Memory.RetrievalStrategy = "hybrid"
		cfg.Memory.MaxContextItems = 10
		cfg.Memory.SimilarityThreshold = 0.60
		cfg.Memory.SeedCSVPath = "D:/Users/Grapari_Infomedia/Downloads/AyuGram Desktop/memories_export.csv"
		cfg.Memory.Embedding.Enabled = false
		cfg.Memory.Embedding.Provider = "openai"
		cfg.Memory.Embedding.Model = "text-embedding-3-small"
		cfg.Memory.Embedding.Dimensions = 1536
 
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

		if envWebshare := os.Getenv("WEBSHARE_API_KEY"); envWebshare != "" {
			cfg.Webshare.APIKey = envWebshare
		}
		if envTavilyKey := os.Getenv("TAVILY_API_KEY"); envTavilyKey != "" {
			cfg.Search.Tavily.APIKey = envTavilyKey
		}
		if envFirecrawlKey := os.Getenv("FIRECRAWL_API_KEY"); envFirecrawlKey != "" {
			cfg.Search.Firecrawl.APIKey = envFirecrawlKey
		}
		if envSearchProv := os.Getenv("SEARCH_PROVIDER"); envSearchProv != "" {
			cfg.Search.Provider = envSearchProv
		}
		if envSearchStrategy := os.Getenv("SEARCH_STRATEGY"); envSearchStrategy != "" {
			cfg.Search.Strategy = envSearchStrategy
		}
		if envWebAdminKey := os.Getenv("WEBADMIN_API_KEY"); envWebAdminKey != "" {
			cfg.WebAdmin.APIKey = envWebAdminKey
		} else if envGoAssistKey := os.Getenv("GOASSISTANT_API_KEY"); envGoAssistKey != "" {
			cfg.WebAdmin.APIKey = envGoAssistKey
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
