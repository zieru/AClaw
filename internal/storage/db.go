package storage

import (
	"database/sql"
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
	_, _ = db.Exec("ALTER TABLE channel_policies ADD COLUMN footer_mode TEXT NOT NULL DEFAULT 'off'")

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
	FooterMode          string    `json:"footer_mode"` // 'off', 'tokens', 'full'
	UpdatedAt           time.Time `json:"updated_at"`
}

type ProviderRecord struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Type         string    `json:"type"` // 9router, openai, anthropic, gemini, groq, deepseek, ollama, custom
	BaseURL      string    `json:"base_url"`
	APIKey       string    `json:"api_key"`
	DefaultModel string    `json:"default_model"`
	IsActive     bool      `json:"is_active"`
	Priority     int       `json:"priority"`
	SettingsJSON string    `json:"settings_json"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
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
	var autoCompInt int
	err := d.db.QueryRow("SELECT id, scope, scope_id, max_upload_file_mb, max_tokens, max_history_turns, auto_compaction, compaction_threshold, model_override, COALESCE(footer_mode, 'off'), updated_at FROM channel_policies WHERE scope = ? AND scope_id = ?", scope, scopeID).
		Scan(&p.ID, &p.Scope, &p.ScopeID, &p.MaxUploadFileMB, &p.MaxTokens, &p.MaxHistoryTurns, &autoCompInt, &p.CompactionThreshold, &p.ModelOverride, &p.FooterMode, &p.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.AutoCompaction = autoCompInt == 1
	return &p, nil
}

func (d *DB) SavePolicy(p *PolicyRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if p.ID == "" {
		p.ID = uuid.New().String()[:8]
	}
	if p.FooterMode == "" {
		p.FooterMode = "off"
	}
	autoCompInt := 0
	if p.AutoCompaction {
		autoCompInt = 1
	}

	query := `
	INSERT INTO channel_policies (id, scope, scope_id, max_upload_file_mb, max_tokens, max_history_turns, auto_compaction, compaction_threshold, model_override, footer_mode, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(scope, scope_id) DO UPDATE SET
		max_upload_file_mb=excluded.max_upload_file_mb,
		max_tokens=excluded.max_tokens,
		max_history_turns=excluded.max_history_turns,
		auto_compaction=excluded.auto_compaction,
		compaction_threshold=excluded.compaction_threshold,
		model_override=excluded.model_override,
		footer_mode=excluded.footer_mode,
		updated_at=CURRENT_TIMESTAMP
	`
	_, err := d.db.Exec(query, p.ID, p.Scope, p.ScopeID, p.MaxUploadFileMB, p.MaxTokens, p.MaxHistoryTurns, autoCompInt, p.CompactionThreshold, p.ModelOverride, p.FooterMode)
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
		FooterMode:          "off",
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
			if chPol.FooterMode != "" {
				res.FooterMode = chPol.FooterMode
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
			if chatPol.FooterMode != "" {
				res.FooterMode = chatPol.FooterMode
			}
		}
	}

	return res
}

// --- Provider Operations ---

func (d *DB) ListProviders() ([]ProviderRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query("SELECT id, name, type, base_url, api_key, default_model, is_active, priority, settings_json, created_at, updated_at FROM providers ORDER BY priority ASC, name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ProviderRecord
	for rows.Next() {
		var p ProviderRecord
		var activeInt int
		if err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &p.APIKey, &p.DefaultModel, &activeInt, &p.Priority, &p.SettingsJSON, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.IsActive = activeInt == 1
		list = append(list, p)
	}
	return list, nil
}

func (d *DB) GetProvider(id string) (*ProviderRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var p ProviderRecord
	var activeInt int
	err := d.db.QueryRow("SELECT id, name, type, base_url, api_key, default_model, is_active, priority, settings_json, created_at, updated_at FROM providers WHERE id = ?", id).
		Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &p.APIKey, &p.DefaultModel, &activeInt, &p.Priority, &p.SettingsJSON, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.IsActive = activeInt == 1
	return &p, nil
}

func (d *DB) SaveProvider(p *ProviderRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if p.ID == "" {
		p.ID = uuid.New().String()[:8]
	}
	activeInt := 0
	if p.IsActive {
		activeInt = 1
	}

	query := `
	INSERT INTO providers (id, name, type, base_url, api_key, default_model, is_active, priority, settings_json, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name,
		type=excluded.type,
		base_url=excluded.base_url,
		api_key=excluded.api_key,
		default_model=excluded.default_model,
		is_active=excluded.is_active,
		priority=excluded.priority,
		settings_json=excluded.settings_json,
		updated_at=CURRENT_TIMESTAMP
	`
	_, err := d.db.Exec(query, p.ID, p.Name, p.Type, p.BaseURL, p.APIKey, p.DefaultModel, activeInt, p.Priority, p.SettingsJSON)
	return err
}

func (d *DB) DeleteProvider(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("DELETE FROM providers WHERE id = ?", id)
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

// --- Audit Log Operations ---

func (d *DB) InsertAuditLog(l *AuditLogRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if l.ID == "" {
		l.ID = uuid.New().String()
	}

	query := `
	INSERT INTO audit_logs (id, timestamp, channel_type, channel_id, chat_id, user_id, user_name, provider, model, prompt_tokens, completion_tokens, total_tokens, latency_ms, cost_usd, tools_called, client_request, system_prompt, full_request_payload, provider_response, status, error_message)
	VALUES (?, CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := d.db.Exec(query, l.ID, l.ChannelType, l.ChannelID, l.ChatID, l.UserID, l.UserName, l.Provider, l.Model, l.PromptTokens, l.CompletionTokens, l.TotalTokens, l.LatencyMs, l.CostUSD, l.ToolsCalled, l.ClientRequest, l.SystemPrompt, l.FullRequestPayload, l.ProviderResponse, l.Status, l.ErrorMessage)
	return err
}

type StatsSummary struct {
	TotalRequests int
	TotalTokens   int
	PromptTokens  int
	CompTokens    int
	TotalCost     float64
	AvgLatencyMs  int
	ErrorCount    int
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
	if err := row.Scan(&s.TotalRequests, &s.TotalTokens, &s.PromptTokens, &s.CompTokens, &s.TotalCost, &avgLatency, &s.ErrorCount); err != nil {
		return nil, err
	}
	s.AvgLatencyMs = int(avgLatency)
	return &s, nil
}

func (d *DB) GetRecentAuditLogs(limit int) ([]AuditLogRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := "SELECT id, timestamp, channel_type, channel_id, chat_id, user_id, user_name, provider, model, prompt_tokens, completion_tokens, total_tokens, latency_ms, cost_usd, tools_called, client_request, system_prompt, full_request_payload, provider_response, status, error_message FROM audit_logs ORDER BY timestamp DESC LIMIT ?"
	rows, err := d.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []AuditLogRecord
	for rows.Next() {
		var l AuditLogRecord
		if err := rows.Scan(&l.ID, &l.Timestamp, &l.ChannelType, &l.ChannelID, &l.ChatID, &l.UserID, &l.UserName, &l.Provider, &l.Model, &l.PromptTokens, &l.CompletionTokens, &l.TotalTokens, &l.LatencyMs, &l.CostUSD, &l.ToolsCalled, &l.ClientRequest, &l.SystemPrompt, &l.FullRequestPayload, &l.ProviderResponse, &l.Status, &l.ErrorMessage); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, nil
}

func (d *DB) GetAuditLogByID(id string) (*AuditLogRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := "SELECT id, timestamp, channel_type, channel_id, chat_id, user_id, user_name, provider, model, prompt_tokens, completion_tokens, total_tokens, latency_ms, cost_usd, tools_called, client_request, system_prompt, full_request_payload, provider_response, status, error_message FROM audit_logs WHERE id = ?"
	row := d.db.QueryRow(query, id)

	var l AuditLogRecord
	if err := row.Scan(&l.ID, &l.Timestamp, &l.ChannelType, &l.ChannelID, &l.ChatID, &l.UserID, &l.UserName, &l.Provider, &l.Model, &l.PromptTokens, &l.CompletionTokens, &l.TotalTokens, &l.LatencyMs, &l.CostUSD, &l.ToolsCalled, &l.ClientRequest, &l.SystemPrompt, &l.FullRequestPayload, &l.ProviderResponse, &l.Status, &l.ErrorMessage); err != nil {
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
