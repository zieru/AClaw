package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite" // Pure Go SQLite driver (Zero-CGO)
)

// DB wraps the SQLite database connection
type DB struct {
	db *sql.DB
	mu sync.RWMutex
}

// Open initializes and migrates the SQLite database
func Open(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	// modernc.org/sqlite connection string with WAL mode and busy timeout
	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Set connection pool settings suitable for SQLite
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	// Execute migrations
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("failed to run schema migrations: %w", err)
	}

	// Auto-upgrade columns if upgrading from earlier version
	_, _ = db.Exec("ALTER TABLE audit_logs ADD COLUMN client_request TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE audit_logs ADD COLUMN system_prompt TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE audit_logs ADD COLUMN full_request_payload TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE audit_logs ADD COLUMN provider_response TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE audit_logs ADD COLUMN tokens_saved INTEGER NOT NULL DEFAULT 0")
	_, _ = db.Exec("ALTER TABLE audit_logs ADD COLUMN proxy_used TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE channel_policies ADD COLUMN footer_mode TEXT NOT NULL DEFAULT 'off'")
	_, _ = db.Exec("ALTER TABLE channel_policies ADD COLUMN token_saver_mode TEXT NOT NULL DEFAULT 'auto'")
	_, _ = db.Exec("ALTER TABLE channel_policies ADD COLUMN proxy_pool_enabled INTEGER NOT NULL DEFAULT 1")
	_, _ = db.Exec("ALTER TABLE providers ADD COLUMN api_keys TEXT NOT NULL DEFAULT '[]'")
	_, _ = db.Exec("ALTER TABLE providers ADD COLUMN models TEXT NOT NULL DEFAULT '[]'")
	_, _ = db.Exec("ALTER TABLE providers ADD COLUMN strategy TEXT NOT NULL DEFAULT 'failsafe'")
	_, _ = db.Exec("ALTER TABLE providers ADD COLUMN key_strategy TEXT NOT NULL DEFAULT 'round-robin'")
	_, _ = db.Exec("ALTER TABLE providers ADD COLUMN proxy_enabled INTEGER NOT NULL DEFAULT 0")
	_, _ = db.Exec("ALTER TABLE providers ADD COLUMN proxy_group TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE proxy_nodes ADD COLUMN group_name TEXT NOT NULL DEFAULT 'default'")
	_, _ = db.Exec("ALTER TABLE channel_policies ADD COLUMN streaming_enabled INTEGER NOT NULL DEFAULT 1")
	_, _ = db.Exec("ALTER TABLE channel_policies ADD COLUMN thinking_enabled INTEGER NOT NULL DEFAULT 1")
	_, _ = db.Exec("ALTER TABLE channel_policies ADD COLUMN thinking_display TEXT NOT NULL DEFAULT 'full'")
	_, _ = db.Exec("ALTER TABLE channel_policies ADD COLUMN timeout_api_seconds INTEGER NOT NULL DEFAULT 0")
	_, _ = db.Exec("ALTER TABLE channel_policies ADD COLUMN max_audit_logs INTEGER NOT NULL DEFAULT 5000")
	_, _ = db.Exec("ALTER TABLE channel_policies ADD COLUMN token_budget INTEGER NOT NULL DEFAULT 0")

	// Ensure default global policy exists and has footer_mode 'full' by default
	_, _ = db.Exec("INSERT OR IGNORE INTO channel_policies (id, scope, scope_id, footer_mode, max_upload_file_mb, max_tokens, max_history_turns, auto_compaction, compaction_threshold) VALUES ('global', 'global', 'system', 'full', 10, 2048, 20, 1, 15)")
	// Ensure any stale non-global policies with default 'off' don't override global
	_, _ = db.Exec("UPDATE channel_policies SET footer_mode = '' WHERE scope != 'global' AND footer_mode = 'off'")

	return &DB{db: db}, nil
}

// Close closes the database connection
func (d *DB) Close() error {
	return d.db.Close()
}

// --- Models ---

type PolicyRecord struct {
	ID                  string    `json:"id"`
	Scope               string    `json:"scope"`    // 'global', 'channel', 'chat'
	ScopeID             string    `json:"scope_id"` // 'system', channel_id, or chat_id
	MaxUploadFileMB     int       `json:"max_upload_file_mb"`
	MaxTokens           int       `json:"max_tokens"`
	MaxHistoryTurns     int       `json:"max_history_turns"`
	AutoCompaction      bool      `json:"auto_compaction"`
	CompactionThreshold int       `json:"compaction_threshold"`
	ModelOverride       string    `json:"model_override"`
	FooterMode          string    `json:"footer_mode"`         // 'off', 'tokens', 'full'
	TokenSaverMode      string    `json:"token_saver_mode"`    // 'off', 'auto', 'aggressive', 'caveman'
	ProxyPoolEnabled    bool      `json:"proxy_pool_enabled"`
	StreamingEnabled    bool      `json:"streaming_enabled"`   // Enable streaming for this scope
	ThinkingEnabled     bool      `json:"thinking_enabled"`    // Enable thinking display
	ThinkingDisplay     string    `json:"thinking_display"`    // 'full', 'summary', 'hidden'
	TimeoutAPISeconds   int       `json:"timeout_api_seconds"` // 0 = default config
	TimeoutHandlerSec   int       `json:"timeout_handler_seconds"` // 0 = default config
	MaxAuditLogs        int       `json:"max_audit_logs"`      // Max rows for rotation, default 5000
	TokenBudget         int       `json:"token_budget"`        // 0 = default
	UpdatedAt           time.Time `json:"updated_at"`
}

type ProxyNodeRecord struct {
	ID           string     `json:"id"`
	URL          string     `json:"url"`
	Protocol     string     `json:"protocol"` // http, https, socks5
	Label        string     `json:"label"`
	GroupName    string     `json:"group_name"`
	IsActive     bool       `json:"is_active"`
	FailCount    int        `json:"fail_count"`
	SuccessCount int        `json:"success_count"`
	AvgLatencyMs int        `json:"avg_latency_ms"`
	LastChecked  *time.Time `json:"last_checked"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type ProviderRecord struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Type         string    `json:"type"` // 9router, openai, anthropic, gemini, groq, deepseek, ollama, custom
	BaseURL      string    `json:"base_url"`
	APIKey       string    `json:"api_key"`
	APIKeys      []string  `json:"api_keys"`
	DefaultModel string    `json:"default_model"`
	Models       []string  `json:"models"`
	Strategy     string    `json:"strategy"`     // failsafe, round-robin, random
	KeyStrategy  string    `json:"key_strategy"` // round-robin, random, failover
	ProxyEnabled bool      `json:"proxy_enabled"`
	ProxyGroup   string    `json:"proxy_group"`
	IsActive     bool      `json:"is_active"`
	Priority     int       `json:"priority"`
	SettingsJSON string    `json:"settings_json"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ComboTarget struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	Priority   int    `json:"priority"`
}

type ModelComboRecord struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Targets     []ComboTarget `json:"targets"`
	Strategy    string        `json:"strategy"` // failsafe, round-robin, random
	IsActive    bool          `json:"is_active"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

type ChannelRecord struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"` // telegram, whatsapp
	Name         string    `json:"name"`
	Identifier   string    `json:"identifier"`
	IsActive     bool      `json:"is_active"`
	DefaultAgent string    `json:"default_agent"`
	DefaultModel string    `json:"default_model"`
	SettingsJSON string    `json:"settings_json"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CronJobRecord struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	CronExpr      string     `json:"cron_expr"`
	TargetChannel string     `json:"target_channel"`
	TargetChatID  string     `json:"target_chat_id"`
	Prompt        string     `json:"prompt"`
	IsActive      bool       `json:"is_active"`
	LastRun       *time.Time `json:"last_run"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type ChatSessionRecord struct {
	ID        string    `json:"id"`
	ChannelID string    `json:"channel_id"`
	ChatID    string    `json:"chat_id"`
	UserID    string    `json:"user_id"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChatMessageRecord struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Tokens    int       `json:"tokens"`
	CreatedAt time.Time `json:"created_at"`
}

type MemoryRecord struct {
	ID        string    `json:"id"`
	Scope     string    `json:"scope"`
	ScopeID   string    `json:"scope_id"`
	KeyTag    string    `json:"key_tag"`
	Content   string    `json:"content"`
	Category  string    `json:"category"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AuditLogRecord struct {
	ID                 string    `json:"id"`
	Timestamp          time.Time `json:"timestamp"`
	ChannelType        string    `json:"channel_type"`
	ChannelID          string    `json:"channel_id"`
	ChatID             string    `json:"chat_id"`
	UserID             string    `json:"user_id"`
	UserName           string    `json:"user_name"`
	Provider           string    `json:"provider"`
	Model              string    `json:"model"`
	PromptTokens       int       `json:"prompt_tokens"`
	CompletionTokens   int       `json:"completion_tokens"`
	TotalTokens        int       `json:"total_tokens"`
	TokensSaved        int       `json:"tokens_saved"`
	ProxyUsed          string    `json:"proxy_used"`
	LatencyMs          int       `json:"latency_ms"`
	CostUSD            float64   `json:"cost_usd"`
	ToolsCalled        string    `json:"tools_called"`
	ClientRequest      string    `json:"client_request"`
	SystemPrompt       string    `json:"system_prompt"`
	FullRequestPayload string    `json:"full_request_payload"`
	ProviderResponse   string    `json:"provider_response"`
	Status             string    `json:"status"`
	ErrorMessage       string    `json:"error_message"`
}

// --- Governance Policy Operations ---

func (d *DB) GetPolicy(scope, scopeID string) (*PolicyRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var p PolicyRecord
	var autoCompInt, proxyPoolInt, streamingInt, thinkingInt int
	err := d.db.QueryRow(`
		SELECT id, scope, scope_id, max_upload_file_mb, max_tokens, max_history_turns, auto_compaction, compaction_threshold, 
		       model_override, COALESCE(footer_mode, ''), COALESCE(token_saver_mode, 'auto'), COALESCE(proxy_pool_enabled, 1),
		       COALESCE(streaming_enabled, 1), COALESCE(thinking_enabled, 1), COALESCE(thinking_display, 'full'),
		       COALESCE(timeout_api_seconds, 0), COALESCE(timeout_handler_seconds, 0), COALESCE(max_audit_logs, 5000), COALESCE(token_budget, 0),
		       updated_at 
		FROM channel_policies WHERE scope = ? AND scope_id = ?`, scope, scopeID).
		Scan(&p.ID, &p.Scope, &p.ScopeID, &p.MaxUploadFileMB, &p.MaxTokens, &p.MaxHistoryTurns, &autoCompInt, &p.CompactionThreshold, 
			&p.ModelOverride, &p.FooterMode, &p.TokenSaverMode, &proxyPoolInt,
			&streamingInt, &thinkingInt, &p.ThinkingDisplay,
			&p.TimeoutAPISeconds, &p.TimeoutHandlerSec, &p.MaxAuditLogs, &p.TokenBudget,
			&p.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if p.Scope == "global" && p.FooterMode == "" {
		p.FooterMode = "full"
	}
	p.AutoCompaction = autoCompInt == 1
	p.ProxyPoolEnabled = proxyPoolInt == 1
	p.StreamingEnabled = streamingInt == 1
	p.ThinkingEnabled = thinkingInt == 1
	return &p, nil
}

func (d *DB) SavePolicy(p *PolicyRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if p.ID == "" {
		p.ID = uuid.New().String()[:8]
	}
	if p.Scope == "global" && p.FooterMode == "" {
		p.FooterMode = "off"
	}
	if p.TokenSaverMode == "" {
		p.TokenSaverMode = "auto"
	}
	if p.ThinkingDisplay == "" {
		p.ThinkingDisplay = "full"
	}
	if p.MaxAuditLogs <= 0 {
		p.MaxAuditLogs = 5000
	}
	autoCompInt := 0
	if p.AutoCompaction {
		autoCompInt = 1
	}
	proxyPoolInt := 0
	if p.ProxyPoolEnabled {
		proxyPoolInt = 1
	}
	streamingInt := 0
	if p.StreamingEnabled {
		streamingInt = 1
	}
	thinkingInt := 0
	if p.ThinkingEnabled {
		thinkingInt = 1
	}

	query := `
	INSERT INTO channel_policies (id, scope, scope_id, max_upload_file_mb, max_tokens, max_history_turns, auto_compaction, compaction_threshold, model_override, footer_mode, token_saver_mode, proxy_pool_enabled, streaming_enabled, thinking_enabled, thinking_display, timeout_api_seconds, timeout_handler_seconds, max_audit_logs, token_budget, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(scope, scope_id) DO UPDATE SET
		max_upload_file_mb=excluded.max_upload_file_mb,
		max_tokens=excluded.max_tokens,
		max_history_turns=excluded.max_history_turns,
		auto_compaction=excluded.auto_compaction,
		compaction_threshold=excluded.compaction_threshold,
		model_override=excluded.model_override,
		footer_mode=excluded.footer_mode,
		token_saver_mode=excluded.token_saver_mode,
		proxy_pool_enabled=excluded.proxy_pool_enabled,
		streaming_enabled=excluded.streaming_enabled,
		thinking_enabled=excluded.thinking_enabled,
		thinking_display=excluded.thinking_display,
		timeout_api_seconds=excluded.timeout_api_seconds,
		timeout_handler_seconds=excluded.timeout_handler_seconds,
		max_audit_logs=excluded.max_audit_logs,
		token_budget=excluded.token_budget,
		updated_at=CURRENT_TIMESTAMP
	`
	_, err := d.db.Exec(query, p.ID, p.Scope, p.ScopeID, p.MaxUploadFileMB, p.MaxTokens, p.MaxHistoryTurns, autoCompInt, p.CompactionThreshold, p.ModelOverride, p.FooterMode, p.TokenSaverMode, proxyPoolInt, streamingInt, thinkingInt, p.ThinkingDisplay, p.TimeoutAPISeconds, p.TimeoutHandlerSec, p.MaxAuditLogs, p.TokenBudget)
	return err
}

// GetResolvedPolicy resolves hierarchical limits: Default -> Global -> Channel -> Chat
func (d *DB) GetResolvedPolicy(channelID, chatID string) PolicyRecord {
	// 1. Defaults
	res := PolicyRecord{
		MaxUploadFileMB:     10,
		MaxTokens:           2048,
		MaxHistoryTurns:     20,
		AutoCompaction:      true,
		CompactionThreshold: 15,
		ModelOverride:       "",
		FooterMode:          "full",
		TokenSaverMode:      "auto",
		ProxyPoolEnabled:    true,
		StreamingEnabled:    true,
		ThinkingEnabled:     true,
		ThinkingDisplay:     "full",
		TimeoutAPISeconds:   0,
		TimeoutHandlerSec:   0,
		MaxAuditLogs:        5000,
		TokenBudget:         0,
	}

	// 2. Global overlay
	if glob, _ := d.GetPolicy("global", "system"); glob != nil {
		if glob.MaxUploadFileMB > 0 {
			res.MaxUploadFileMB = glob.MaxUploadFileMB
		}
		if glob.MaxTokens > 0 {
			res.MaxTokens = glob.MaxTokens
		}
		if glob.MaxHistoryTurns > 0 {
			res.MaxHistoryTurns = glob.MaxHistoryTurns
		}
		res.AutoCompaction = glob.AutoCompaction
		if glob.CompactionThreshold > 0 {
			res.CompactionThreshold = glob.CompactionThreshold
		}
		if glob.ModelOverride != "" {
			res.ModelOverride = glob.ModelOverride
		}
		if glob.FooterMode != "" {
			res.FooterMode = glob.FooterMode
		}
		if glob.TokenSaverMode != "" {
			res.TokenSaverMode = glob.TokenSaverMode
		}
		res.ProxyPoolEnabled = glob.ProxyPoolEnabled
		res.StreamingEnabled = glob.StreamingEnabled
		res.ThinkingEnabled = glob.ThinkingEnabled
		if glob.ThinkingDisplay != "" {
			res.ThinkingDisplay = glob.ThinkingDisplay
		}
		if glob.TimeoutAPISeconds > 0 {
			res.TimeoutAPISeconds = glob.TimeoutAPISeconds
		}
		if glob.TimeoutHandlerSec > 0 {
			res.TimeoutHandlerSec = glob.TimeoutHandlerSec
		}
		if glob.MaxAuditLogs > 0 {
			res.MaxAuditLogs = glob.MaxAuditLogs
		}
		if glob.TokenBudget > 0 {
			res.TokenBudget = glob.TokenBudget
		}
	}

	// 3. Channel overlay
	if channelID != "" {
		if chPol, _ := d.GetPolicy("channel", channelID); chPol != nil {
			if chPol.MaxUploadFileMB > 0 {
				res.MaxUploadFileMB = chPol.MaxUploadFileMB
			}
			if chPol.MaxTokens > 0 {
				res.MaxTokens = chPol.MaxTokens
			}
			if chPol.MaxHistoryTurns > 0 {
				res.MaxHistoryTurns = chPol.MaxHistoryTurns
			}
			res.AutoCompaction = chPol.AutoCompaction
			if chPol.CompactionThreshold > 0 {
				res.CompactionThreshold = chPol.CompactionThreshold
			}
			if chPol.ModelOverride != "" {
				res.ModelOverride = chPol.ModelOverride
			}
			if chPol.FooterMode != "" && chPol.FooterMode != "inherit" {
				res.FooterMode = chPol.FooterMode
			}
			if chPol.TokenSaverMode != "" {
				res.TokenSaverMode = chPol.TokenSaverMode
			}
			res.ProxyPoolEnabled = chPol.ProxyPoolEnabled
			res.StreamingEnabled = chPol.StreamingEnabled
			res.ThinkingEnabled = chPol.ThinkingEnabled
			if chPol.ThinkingDisplay != "" {
				res.ThinkingDisplay = chPol.ThinkingDisplay
			}
			if chPol.TimeoutAPISeconds > 0 {
				res.TimeoutAPISeconds = chPol.TimeoutAPISeconds
			}
			if chPol.TimeoutHandlerSec > 0 {
				res.TimeoutHandlerSec = chPol.TimeoutHandlerSec
			}
			if chPol.MaxAuditLogs > 0 {
				res.MaxAuditLogs = chPol.MaxAuditLogs
			}
			if chPol.TokenBudget > 0 {
				res.TokenBudget = chPol.TokenBudget
			}
		}
	}

	// 4. Chat/Group overlay
	if chatID != "" {
		if chatPol, _ := d.GetPolicy("chat", chatID); chatPol != nil {
			if chatPol.MaxUploadFileMB > 0 {
				res.MaxUploadFileMB = chatPol.MaxUploadFileMB
			}
			if chatPol.MaxTokens > 0 {
				res.MaxTokens = chatPol.MaxTokens
			}
			if chatPol.MaxHistoryTurns > 0 {
				res.MaxHistoryTurns = chatPol.MaxHistoryTurns
			}
			res.AutoCompaction = chatPol.AutoCompaction
			if chatPol.CompactionThreshold > 0 {
				res.CompactionThreshold = chatPol.CompactionThreshold
			}
			if chatPol.ModelOverride != "" {
				res.ModelOverride = chatPol.ModelOverride
			}
			if chatPol.FooterMode != "" && chatPol.FooterMode != "inherit" {
				res.FooterMode = chatPol.FooterMode
			}
			if chatPol.TokenSaverMode != "" {
				res.TokenSaverMode = chatPol.TokenSaverMode
			}
			res.ProxyPoolEnabled = chatPol.ProxyPoolEnabled
			res.StreamingEnabled = chatPol.StreamingEnabled
			res.ThinkingEnabled = chatPol.ThinkingEnabled
			if chatPol.ThinkingDisplay != "" {
				res.ThinkingDisplay = chatPol.ThinkingDisplay
			}
			if chatPol.TimeoutAPISeconds > 0 {
				res.TimeoutAPISeconds = chatPol.TimeoutAPISeconds
			}
			if chatPol.TimeoutHandlerSec > 0 {
				res.TimeoutHandlerSec = chatPol.TimeoutHandlerSec
			}
			if chatPol.MaxAuditLogs > 0 {
				res.MaxAuditLogs = chatPol.MaxAuditLogs
			}
			if chatPol.TokenBudget > 0 {
				res.TokenBudget = chatPol.TokenBudget
			}
		}
	}

	return res
}

// --- Provider Operations ---

func (d *DB) ListProviders() ([]ProviderRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`
		SELECT id, name, type, base_url, api_key, COALESCE(api_keys, '[]'), default_model, COALESCE(models, '[]'), 
		       COALESCE(strategy, 'failsafe'), COALESCE(key_strategy, 'round-robin'), 
		       COALESCE(proxy_enabled, 0), COALESCE(proxy_group, ''),
		       is_active, priority, settings_json, created_at, updated_at 
		FROM providers ORDER BY priority ASC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ProviderRecord
	for rows.Next() {
		var p ProviderRecord
		var activeInt, proxyEnabledInt int
		var apiKeysJSON, modelsJSON string
		if err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &p.APIKey, &apiKeysJSON, &p.DefaultModel, &modelsJSON, &p.Strategy, &p.KeyStrategy, &proxyEnabledInt, &p.ProxyGroup, &activeInt, &p.Priority, &p.SettingsJSON, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.IsActive = activeInt == 1
		p.ProxyEnabled = proxyEnabledInt == 1
		_ = json.Unmarshal([]byte(apiKeysJSON), &p.APIKeys)
		_ = json.Unmarshal([]byte(modelsJSON), &p.Models)
		if len(p.APIKeys) == 0 && p.APIKey != "" {
			p.APIKeys = []string{p.APIKey}
		}
		list = append(list, p)
	}
	return list, nil
}

func (d *DB) GetProvider(id string) (*ProviderRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var p ProviderRecord
	var activeInt, proxyEnabledInt int
	var apiKeysJSON, modelsJSON string
	err := d.db.QueryRow(`
		SELECT id, name, type, base_url, api_key, COALESCE(api_keys, '[]'), default_model, COALESCE(models, '[]'), 
		       COALESCE(strategy, 'failsafe'), COALESCE(key_strategy, 'round-robin'), 
		       COALESCE(proxy_enabled, 0), COALESCE(proxy_group, ''),
		       is_active, priority, settings_json, created_at, updated_at 
		FROM providers WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &p.APIKey, &apiKeysJSON, &p.DefaultModel, &modelsJSON, &p.Strategy, &p.KeyStrategy, &proxyEnabledInt, &p.ProxyGroup, &activeInt, &p.Priority, &p.SettingsJSON, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.IsActive = activeInt == 1
	p.ProxyEnabled = proxyEnabledInt == 1
	_ = json.Unmarshal([]byte(apiKeysJSON), &p.APIKeys)
	_ = json.Unmarshal([]byte(modelsJSON), &p.Models)
	if len(p.APIKeys) == 0 && p.APIKey != "" {
		p.APIKeys = []string{p.APIKey}
	}
	return &p, nil
}

func (d *DB) SaveProvider(p *ProviderRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if p.ID == "" {
		p.ID = uuid.New().String()[:8]
	}
	if p.Strategy == "" {
		p.Strategy = "failsafe"
	}
	if p.KeyStrategy == "" {
		p.KeyStrategy = "round-robin"
	}
	if len(p.APIKeys) == 0 && p.APIKey != "" {
		p.APIKeys = []string{p.APIKey}
	}
	if p.APIKey == "" && len(p.APIKeys) > 0 {
		p.APIKey = p.APIKeys[0]
	}

	apiKeysBytes, _ := json.Marshal(p.APIKeys)
	modelsBytes, _ := json.Marshal(p.Models)

	activeInt := 0
	if p.IsActive {
		activeInt = 1
	}
	proxyEnabledInt := 0
	if p.ProxyEnabled {
		proxyEnabledInt = 1
	}

	query := `
	INSERT INTO providers (id, name, type, base_url, api_key, api_keys, default_model, models, strategy, key_strategy, proxy_enabled, proxy_group, is_active, priority, settings_json, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name,
		type=excluded.type,
		base_url=excluded.base_url,
		api_key=excluded.api_key,
		api_keys=excluded.api_keys,
		default_model=excluded.default_model,
		models=excluded.models,
		strategy=excluded.strategy,
		key_strategy=excluded.key_strategy,
		proxy_enabled=excluded.proxy_enabled,
		proxy_group=excluded.proxy_group,
		is_active=excluded.is_active,
		priority=excluded.priority,
		settings_json=excluded.settings_json,
		updated_at=CURRENT_TIMESTAMP
	`
	_, err := d.db.Exec(query, p.ID, p.Name, p.Type, p.BaseURL, p.APIKey, string(apiKeysBytes), p.DefaultModel, string(modelsBytes), p.Strategy, p.KeyStrategy, proxyEnabledInt, p.ProxyGroup, activeInt, p.Priority, p.SettingsJSON)
	return err
}

func (d *DB) DeleteProvider(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("DELETE FROM providers WHERE id = ?", id)
	return err
}

// --- Model Combo Operations ---

func (d *DB) ListCombos() ([]ModelComboRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query("SELECT id, name, description, targets_json, strategy, is_active, created_at, updated_at FROM model_combos ORDER BY name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ModelComboRecord
	for rows.Next() {
		var c ModelComboRecord
		var targetsJSON string
		var activeInt int
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &targetsJSON, &c.Strategy, &activeInt, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.IsActive = activeInt == 1
		_ = json.Unmarshal([]byte(targetsJSON), &c.Targets)
		list = append(list, c)
	}
	return list, nil
}

func (d *DB) GetCombo(name string) (*ModelComboRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var c ModelComboRecord
	var targetsJSON string
	var activeInt int
	err := d.db.QueryRow("SELECT id, name, description, targets_json, strategy, is_active, created_at, updated_at FROM model_combos WHERE name = ?", name).
		Scan(&c.ID, &c.Name, &c.Description, &targetsJSON, &c.Strategy, &activeInt, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.IsActive = activeInt == 1
	_ = json.Unmarshal([]byte(targetsJSON), &c.Targets)
	return &c, nil
}

func (d *DB) SaveCombo(c *ModelComboRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if c.ID == "" {
		c.ID = uuid.New().String()[:8]
	}
	if c.Strategy == "" {
		c.Strategy = "failsafe"
	}
	activeInt := 0
	if c.IsActive {
		activeInt = 1
	}

	targetsBytes, _ := json.Marshal(c.Targets)

	query := `
	INSERT INTO model_combos (id, name, description, targets_json, strategy, is_active, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(name) DO UPDATE SET
		description=excluded.description,
		targets_json=excluded.targets_json,
		strategy=excluded.strategy,
		is_active=excluded.is_active,
		updated_at=CURRENT_TIMESTAMP
	`
	_, err := d.db.Exec(query, c.ID, c.Name, c.Description, string(targetsBytes), c.Strategy, activeInt)
	return err
}

func (d *DB) DeleteCombo(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("DELETE FROM model_combos WHERE name = ?", name)
	return err
}

// --- Channel Operations ---

func (d *DB) ListChannels() ([]ChannelRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query("SELECT id, type, name, identifier, is_active, default_agent, default_model, settings_json, created_at, updated_at FROM channels ORDER BY name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ChannelRecord
	for rows.Next() {
		var c ChannelRecord
		var activeInt int
		if err := rows.Scan(&c.ID, &c.Type, &c.Name, &c.Identifier, &activeInt, &c.DefaultAgent, &c.DefaultModel, &c.SettingsJSON, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.IsActive = activeInt == 1
		list = append(list, c)
	}
	return list, nil
}

func (d *DB) GetChannel(id string) (*ChannelRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var c ChannelRecord
	var activeInt int
	err := d.db.QueryRow("SELECT id, type, name, identifier, is_active, default_agent, default_model, settings_json, created_at, updated_at FROM channels WHERE id = ?", id).
		Scan(&c.ID, &c.Type, &c.Name, &c.Identifier, &activeInt, &c.DefaultAgent, &c.DefaultModel, &c.SettingsJSON, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.IsActive = activeInt == 1
	return &c, nil
}

func (d *DB) SaveChannel(c *ChannelRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if c.ID == "" {
		c.ID = uuid.New().String()[:8]
	}
	activeInt := 0
	if c.IsActive {
		activeInt = 1
	}

	query := `
	INSERT INTO channels (id, type, name, identifier, is_active, default_agent, default_model, settings_json, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(id) DO UPDATE SET
		type=excluded.type,
		name=excluded.name,
		identifier=excluded.identifier,
		is_active=excluded.is_active,
		default_agent=excluded.default_agent,
		default_model=excluded.default_model,
		settings_json=excluded.settings_json,
		updated_at=CURRENT_TIMESTAMP
	`
	_, err := d.db.Exec(query, c.ID, c.Type, c.Name, c.Identifier, activeInt, c.DefaultAgent, c.DefaultModel, c.SettingsJSON)
	return err
}

func (d *DB) DeleteChannel(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("DELETE FROM channels WHERE id = ?", id)
	return err
}

// --- Channel Tool Permissions ---

func (d *DB) GetChannelToolPerms(channelID string) (map[string]bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query("SELECT tool_name, is_allowed FROM channel_tool_perms WHERE channel_id = ?", channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	perms := make(map[string]bool)
	for rows.Next() {
		var toolName string
		var allowedInt int
		if err := rows.Scan(&toolName, &allowedInt); err != nil {
			return nil, err
		}
		perms[toolName] = allowedInt == 1
	}
	return perms, nil
}

func (d *DB) SetChannelToolPerm(channelID, toolName string, isAllowed bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	allowedInt := 0
	if isAllowed {
		allowedInt = 1
	}

	query := `
	INSERT INTO channel_tool_perms (channel_id, tool_name, is_allowed, updated_at)
	VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(channel_id, tool_name) DO UPDATE SET
		is_allowed=excluded.is_allowed,
		updated_at=CURRENT_TIMESTAMP
	`
	_, err := d.db.Exec(query, channelID, toolName, allowedInt)
	return err
}

// --- Cron Operations ---

func (d *DB) ListCronJobs() ([]CronJobRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query("SELECT id, name, cron_expr, target_channel, target_chat_id, prompt, is_active, last_run, created_at, updated_at FROM cron_jobs ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []CronJobRecord
	for rows.Next() {
		var j CronJobRecord
		var activeInt int
		if err := rows.Scan(&j.ID, &j.Name, &j.CronExpr, &j.TargetChannel, &j.TargetChatID, &j.Prompt, &activeInt, &j.LastRun, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		j.IsActive = activeInt == 1
		list = append(list, j)
	}
	return list, nil
}

func (d *DB) GetCronJob(id string) (*CronJobRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var j CronJobRecord
	var activeInt int
	err := d.db.QueryRow("SELECT id, name, cron_expr, target_channel, target_chat_id, prompt, is_active, last_run, created_at, updated_at FROM cron_jobs WHERE id = ?", id).
		Scan(&j.ID, &j.Name, &j.CronExpr, &j.TargetChannel, &j.TargetChatID, &j.Prompt, &activeInt, &j.LastRun, &j.CreatedAt, &j.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.IsActive = activeInt == 1
	return &j, nil
}

func (d *DB) SaveCronJob(j *CronJobRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if j.ID == "" {
		j.ID = uuid.New().String()[:8]
	}
	activeInt := 0
	if j.IsActive {
		activeInt = 1
	}

	query := `
	INSERT INTO cron_jobs (id, name, cron_expr, target_channel, target_chat_id, prompt, is_active, last_run, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name,
		cron_expr=excluded.cron_expr,
		target_channel=excluded.target_channel,
		target_chat_id=excluded.target_chat_id,
		prompt=excluded.prompt,
		is_active=excluded.is_active,
		last_run=excluded.last_run,
		updated_at=CURRENT_TIMESTAMP
	`
	_, err := d.db.Exec(query, j.ID, j.Name, j.CronExpr, j.TargetChannel, j.TargetChatID, j.Prompt, activeInt, j.LastRun)
	return err
}

func (d *DB) DeleteCronJob(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("DELETE FROM cron_jobs WHERE id = ?", id)
	return err
}

func (d *DB) UpdateCronLastRun(id string, t time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("UPDATE cron_jobs SET last_run = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", t, id)
	return err
}

// --- Session & Memory Operations ---

func (d *DB) GetOrCreateSession(channelID, chatID, userID string) (*ChatSessionRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var s ChatSessionRecord
	err := d.db.QueryRow("SELECT id, channel_id, chat_id, user_id, title, summary, created_at, updated_at FROM chat_sessions WHERE channel_id = ? AND chat_id = ?", channelID, chatID).
		Scan(&s.ID, &s.ChannelID, &s.ChatID, &s.UserID, &s.Title, &s.Summary, &s.CreatedAt, &s.UpdatedAt)

	if err == nil {
		return &s, nil
	}

	if err != sql.ErrNoRows {
		return nil, err
	}

	// Create new session
	s = ChatSessionRecord{
		ID:        uuid.New().String(),
		ChannelID: channelID,
		ChatID:    chatID,
		UserID:    userID,
		Title:     "New Conversation",
		Summary:   "",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	_, err = d.db.Exec("INSERT INTO chat_sessions (id, channel_id, chat_id, user_id, title, summary) VALUES (?, ?, ?, ?, ?, ?)",
		s.ID, s.ChannelID, s.ChatID, s.UserID, s.Title, s.Summary)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (d *DB) AddMessage(sessionID, role, content string, tokens int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	id := uuid.New().String()
	_, err := d.db.Exec("INSERT INTO chat_messages (id, session_id, role, content, tokens) VALUES (?, ?, ?, ?, ?)",
		id, sessionID, role, content, tokens)
	return err
}

func (d *DB) CountSessionMessages(sessionID string) (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM chat_messages WHERE session_id = ?", sessionID).Scan(&count)
	return count, err
}

func (d *DB) TruncateOldMessages(sessionID string, keepCount int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	query := `
	DELETE FROM chat_messages 
	WHERE session_id = ? AND id NOT IN (
		SELECT id FROM chat_messages WHERE session_id = ? ORDER BY created_at DESC LIMIT ?
	)
	`
	_, err := d.db.Exec(query, sessionID, sessionID, keepCount)
	return err
}

func (d *DB) GetRecentMessages(sessionID string, limit int) ([]ChatMessageRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := "SELECT id, session_id, role, content, tokens, created_at FROM chat_messages WHERE session_id = ? ORDER BY created_at DESC LIMIT ?"
	rows, err := d.db.Query(query, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ChatMessageRecord
	for rows.Next() {
		var m ChatMessageRecord
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Tokens, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}

	// Reverse to get chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func (d *DB) ClearSessionMessages(sessionID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec("DELETE FROM chat_messages WHERE session_id = ?", sessionID)
	if err != nil {
		return err
	}
	_, err = d.db.Exec("UPDATE chat_sessions SET summary = '', updated_at = CURRENT_TIMESTAMP WHERE id = ?", sessionID)
	return err
}

func (d *DB) ClearSessionsByChatID(chatID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.db.Query("SELECT id FROM chat_sessions WHERE chat_id = ?", chatID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var sessionIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			sessionIDs = append(sessionIDs, id)
		}
	}

	for _, sid := range sessionIDs {
		_, _ = d.db.Exec("DELETE FROM chat_messages WHERE session_id = ?", sid)
		_, _ = d.db.Exec("UPDATE chat_sessions SET summary = '', updated_at = CURRENT_TIMESTAMP WHERE id = ?", sid)
	}
	return nil
}

func (d *DB) UpdateSessionSummary(sessionID, summary string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("UPDATE chat_sessions SET summary = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", summary, sessionID)
	return err
}

// --- Memory Items ---

func (d *DB) AddMemoryItem(scope, scopeID, keyTag, content, category string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	id := uuid.New().String()
	_, err := d.db.Exec("INSERT INTO memory_items (id, scope, scope_id, key_tag, content, category) VALUES (?, ?, ?, ?, ?, ?)",
		id, scope, scopeID, keyTag, content, category)
	return err
}

func (d *DB) ListMemoryItems(scope, scopeID string) ([]MemoryRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query("SELECT id, scope, scope_id, key_tag, content, category, created_at, updated_at FROM memory_items WHERE scope = ? AND scope_id = ? ORDER BY updated_at DESC", scope, scopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []MemoryRecord
	for rows.Next() {
		var m MemoryRecord
		if err := rows.Scan(&m.ID, &m.Scope, &m.ScopeID, &m.KeyTag, &m.Content, &m.Category, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, nil
}

func (d *DB) DeleteMemoryItem(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("DELETE FROM memory_items WHERE id = ?", id)
	return err
}

// --- Proxy Node Operations ---

func (d *DB) ListProxyNodes() ([]ProxyNodeRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query("SELECT id, url, protocol, label, COALESCE(group_name, 'default'), is_active, fail_count, success_count, avg_latency_ms, last_checked, created_at, updated_at FROM proxy_nodes ORDER BY is_active DESC, group_name ASC, avg_latency_ms ASC, created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ProxyNodeRecord
	for rows.Next() {
		var n ProxyNodeRecord
		var activeInt int
		if err := rows.Scan(&n.ID, &n.URL, &n.Protocol, &n.Label, &n.GroupName, &activeInt, &n.FailCount, &n.SuccessCount, &n.AvgLatencyMs, &n.LastChecked, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		n.IsActive = activeInt == 1
		list = append(list, n)
	}
	return list, nil
}

func (d *DB) ListProxyNodesByGroup(group string) ([]ProxyNodeRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query("SELECT id, url, protocol, label, COALESCE(group_name, 'default'), is_active, fail_count, success_count, avg_latency_ms, last_checked, created_at, updated_at FROM proxy_nodes WHERE group_name = ? ORDER BY is_active DESC, avg_latency_ms ASC", group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ProxyNodeRecord
	for rows.Next() {
		var n ProxyNodeRecord
		var activeInt int
		if err := rows.Scan(&n.ID, &n.URL, &n.Protocol, &n.Label, &n.GroupName, &activeInt, &n.FailCount, &n.SuccessCount, &n.AvgLatencyMs, &n.LastChecked, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		n.IsActive = activeInt == 1
		list = append(list, n)
	}
	return list, nil
}

func (d *DB) ListProxyGroups() ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query("SELECT DISTINCT COALESCE(group_name, 'default') FROM proxy_nodes ORDER BY group_name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err == nil && g != "" {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		groups = []string{"default"}
	}
	return groups, nil
}

func (d *DB) GetProxyNode(id string) (*ProxyNodeRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var n ProxyNodeRecord
	var activeInt int
	err := d.db.QueryRow("SELECT id, url, protocol, label, COALESCE(group_name, 'default'), is_active, fail_count, success_count, avg_latency_ms, last_checked, created_at, updated_at FROM proxy_nodes WHERE id = ?", id).
		Scan(&n.ID, &n.URL, &n.Protocol, &n.Label, &n.GroupName, &activeInt, &n.FailCount, &n.SuccessCount, &n.AvgLatencyMs, &n.LastChecked, &n.CreatedAt, &n.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n.IsActive = activeInt == 1
	return &n, nil
}

func (d *DB) SaveProxyNode(n *ProxyNodeRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if n.ID == "" {
		n.ID = uuid.New().String()[:8]
	}
	if n.Protocol == "" {
		n.Protocol = "http"
	}
	if n.GroupName == "" {
		n.GroupName = "default"
	}
	activeInt := 0
	if n.IsActive {
		activeInt = 1
	}

	query := `
	INSERT INTO proxy_nodes (id, url, protocol, label, group_name, is_active, fail_count, success_count, avg_latency_ms, last_checked, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(id) DO UPDATE SET
		url=excluded.url,
		protocol=excluded.protocol,
		label=excluded.label,
		group_name=excluded.group_name,
		is_active=excluded.is_active,
		fail_count=excluded.fail_count,
		success_count=excluded.success_count,
		avg_latency_ms=excluded.avg_latency_ms,
		last_checked=excluded.last_checked,
		updated_at=CURRENT_TIMESTAMP
	`
	_, err := d.db.Exec(query, n.ID, n.URL, n.Protocol, n.Label, n.GroupName, activeInt, n.FailCount, n.SuccessCount, n.AvgLatencyMs, n.LastChecked)
	return err
}

func (d *DB) SaveBatchProxies(nodes []*ProxyNodeRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO proxy_nodes (id, url, protocol, label, group_name, is_active, fail_count, success_count, avg_latency_ms, last_checked, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			url=excluded.url,
			protocol=excluded.protocol,
			label=excluded.label,
			group_name=excluded.group_name,
			is_active=excluded.is_active,
			updated_at=CURRENT_TIMESTAMP
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, n := range nodes {
		if n.ID == "" {
			n.ID = uuid.New().String()[:8]
		}
		if n.Protocol == "" {
			n.Protocol = "http"
		}
		if n.GroupName == "" {
			n.GroupName = "default"
		}
		activeInt := 0
		if n.IsActive {
			activeInt = 1
		}
		if _, err := stmt.Exec(n.ID, n.URL, n.Protocol, n.Label, n.GroupName, activeInt, n.FailCount, n.SuccessCount, n.AvgLatencyMs, n.LastChecked); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (d *DB) ToggleProxyGroup(group string, enable bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	activeInt := 0
	if enable {
		activeInt = 1
	}
	_, err := d.db.Exec("UPDATE proxy_nodes SET is_active = ?, updated_at = CURRENT_TIMESTAMP WHERE group_name = ?", activeInt, group)
	return err
}

func (d *DB) DeleteProxyGroup(group string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("DELETE FROM proxy_nodes WHERE group_name = ?", group)
	return err
}

func (d *DB) DeleteProxyNode(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("DELETE FROM proxy_nodes WHERE id = ?", id)
	return err
}

func (d *DB) UpdateProxyStats(id string, success bool, latencyMs int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	if success {
		query := `
		UPDATE proxy_nodes 
		SET success_count = success_count + 1,
		    avg_latency_ms = CASE WHEN avg_latency_ms = 0 THEN ? ELSE (avg_latency_ms * 3 + ?) / 4 END,
		    last_checked = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
		`
		_, err := d.db.Exec(query, latencyMs, latencyMs, now, id)
		return err
	}

	query := `
	UPDATE proxy_nodes 
	SET fail_count = fail_count + 1,
	    last_checked = ?,
	    updated_at = CURRENT_TIMESTAMP
	WHERE id = ?
	`
	_, err := d.db.Exec(query, now, id)
	return err
}

// --- Audit Log Operations ---

func (d *DB) InsertAuditLog(l *AuditLogRecord) error {
	d.mu.Lock()
	if l.ID == "" {
		l.ID = uuid.New().String()
	}

	query := `
	INSERT INTO audit_logs (id, timestamp, channel_type, channel_id, chat_id, user_id, user_name, provider, model, prompt_tokens, completion_tokens, total_tokens, tokens_saved, proxy_used, latency_ms, cost_usd, tools_called, client_request, system_prompt, full_request_payload, provider_response, status, error_message)
	VALUES (?, CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := d.db.Exec(query, l.ID, l.ChannelType, l.ChannelID, l.ChatID, l.UserID, l.UserName, l.Provider, l.Model, l.PromptTokens, l.CompletionTokens, l.TotalTokens, l.TokensSaved, l.ProxyUsed, l.LatencyMs, l.CostUSD, l.ToolsCalled, l.ClientRequest, l.SystemPrompt, l.FullRequestPayload, l.ProviderResponse, l.Status, l.ErrorMessage)
	d.mu.Unlock()

	if err == nil {
		// Auto-rotate in background if needed
		go func() {
			maxLogs := 5000
			if glob, err := d.GetPolicy("global", "system"); err == nil && glob != nil && glob.MaxAuditLogs > 0 {
				maxLogs = glob.MaxAuditLogs
			}
			_, _ = d.RotateAuditLogs(maxLogs)
		}()
	}

	return err
}

// RotateAuditLogs prunes oldest audit logs keeping only the most recent maxCount records
func (d *DB) RotateAuditLogs(maxCount int) (int64, error) {
	if maxCount <= 0 {
		return 0, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	query := `
	DELETE FROM audit_logs 
	WHERE id NOT IN (
		SELECT id FROM audit_logs 
		ORDER BY timestamp DESC, id DESC 
		LIMIT ?
	)
	`
	res, err := d.db.Exec(query, maxCount)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountAuditLogs returns total number of records in audit_logs table
func (d *DB) CountAuditLogs() (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM audit_logs").Scan(&count)
	return count, err
}

// CountActiveSessions returns total number of chat sessions
func (d *DB) CountActiveSessions() (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM chat_sessions").Scan(&count)
	return count, err
}

type StatsSummary struct {
	TotalRequests    int
	TotalTokens      int
	TotalTokensSaved int
	PromptTokens     int
	CompTokens       int
	TotalCost        float64
	AvgLatencyMs     int
	ErrorCount       int
}

func (d *DB) GetStatsSummary(since time.Time) (*StatsSummary, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	sinceStr := "1970-01-01 00:00:00"
	if !since.IsZero() {
		sinceStr = since.UTC().Format("2006-01-02 15:04:05")
	}

	query := `
	SELECT 
		COUNT(*),
		COALESCE(SUM(total_tokens), 0),
		COALESCE(SUM(tokens_saved), 0),
		COALESCE(SUM(prompt_tokens), 0),
		COALESCE(SUM(completion_tokens), 0),
		COALESCE(SUM(cost_usd), 0.0),
		COALESCE(AVG(latency_ms), 0.0),
		COALESCE(SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END), 0)
	FROM audit_logs
	WHERE timestamp >= ?
	`
	row := d.db.QueryRow(query, sinceStr)
	var s StatsSummary
	var avgLatency float64
	if err := row.Scan(&s.TotalRequests, &s.TotalTokens, &s.TotalTokensSaved, &s.PromptTokens, &s.CompTokens, &s.TotalCost, &avgLatency, &s.ErrorCount); err != nil {
		return nil, err
	}
	s.AvgLatencyMs = int(avgLatency)
	return &s, nil
}

func (d *DB) GetRecentAuditLogs(limit int) ([]AuditLogRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := "SELECT id, timestamp, channel_type, channel_id, chat_id, user_id, user_name, provider, model, prompt_tokens, completion_tokens, total_tokens, COALESCE(tokens_saved, 0), COALESCE(proxy_used, ''), latency_ms, cost_usd, tools_called, client_request, system_prompt, full_request_payload, provider_response, status, error_message FROM audit_logs ORDER BY timestamp DESC LIMIT ?"
	rows, err := d.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []AuditLogRecord
	for rows.Next() {
		var l AuditLogRecord
		if err := rows.Scan(&l.ID, &l.Timestamp, &l.ChannelType, &l.ChannelID, &l.ChatID, &l.UserID, &l.UserName, &l.Provider, &l.Model, &l.PromptTokens, &l.CompletionTokens, &l.TotalTokens, &l.TokensSaved, &l.ProxyUsed, &l.LatencyMs, &l.CostUSD, &l.ToolsCalled, &l.ClientRequest, &l.SystemPrompt, &l.FullRequestPayload, &l.ProviderResponse, &l.Status, &l.ErrorMessage); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, nil
}

func (d *DB) GetAuditLogByID(id string) (*AuditLogRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := "SELECT id, timestamp, channel_type, channel_id, chat_id, user_id, user_name, provider, model, prompt_tokens, completion_tokens, total_tokens, COALESCE(tokens_saved, 0), COALESCE(proxy_used, ''), latency_ms, cost_usd, tools_called, client_request, system_prompt, full_request_payload, provider_response, status, error_message FROM audit_logs WHERE id = ?"
	row := d.db.QueryRow(query, id)

	var l AuditLogRecord
	if err := row.Scan(&l.ID, &l.Timestamp, &l.ChannelType, &l.ChannelID, &l.ChatID, &l.UserID, &l.UserName, &l.Provider, &l.Model, &l.PromptTokens, &l.CompletionTokens, &l.TotalTokens, &l.TokensSaved, &l.ProxyUsed, &l.LatencyMs, &l.CostUSD, &l.ToolsCalled, &l.ClientRequest, &l.SystemPrompt, &l.FullRequestPayload, &l.ProviderResponse, &l.Status, &l.ErrorMessage); err != nil {
		return nil, err
	}
	return &l, nil
}

// --- System Settings ---

func (d *DB) GetSetting(key string, fallback string) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var val string
	err := d.db.QueryRow("SELECT value FROM system_settings WHERE key = ?", key).Scan(&val)
	if err == sql.ErrNoRows {
		return fallback, nil
	}
	if err != nil {
		return fallback, err
	}
	return val, nil
}

func (d *DB) SetSetting(key, val string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	query := `
	INSERT INTO system_settings (key, value, updated_at)
	VALUES (?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP
	`
	_, err := d.db.Exec(query, key, val)
	return err
}
