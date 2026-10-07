package storage

const schemaSQL = `
CREATE TABLE IF NOT EXISTS system_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS channel_policies (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL, -- 'global', 'channel', 'chat'
    scope_id TEXT NOT NULL, -- 'system', channel_id, or chat_id/group_id
    max_upload_file_mb INTEGER NOT NULL DEFAULT 10,
    max_tokens INTEGER NOT NULL DEFAULT 2048,
    max_history_turns INTEGER NOT NULL DEFAULT 20,
    auto_compaction INTEGER NOT NULL DEFAULT 1,
    compaction_threshold INTEGER NOT NULL DEFAULT 15,
    model_override TEXT NOT NULL DEFAULT '',
    footer_mode TEXT NOT NULL DEFAULT 'off',
    token_saver_mode TEXT NOT NULL DEFAULT 'auto', -- 'off', 'auto', 'aggressive', 'caveman'
    proxy_pool_enabled INTEGER NOT NULL DEFAULT 1,
    timeout_api_seconds INTEGER NOT NULL DEFAULT 0,
    timeout_handler_seconds INTEGER NOT NULL DEFAULT 0,
    max_audit_logs INTEGER NOT NULL DEFAULT 5000,
    token_budget INTEGER NOT NULL DEFAULT 0,
    response_cache_enabled INTEGER NOT NULL DEFAULT 1,
    response_cache_ttl_sec INTEGER NOT NULL DEFAULT 1800,
    fallback_vision_model TEXT NOT NULL DEFAULT '',
    fallback_audio_model TEXT NOT NULL DEFAULT '',
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(scope, scope_id)
);

CREATE TABLE IF NOT EXISTS providers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL, -- 9router, openai, anthropic, gemini, groq, deepseek, ollama, custom
    base_url TEXT NOT NULL DEFAULT '',
    api_key TEXT NOT NULL DEFAULT '',
    api_keys TEXT NOT NULL DEFAULT '[]', -- JSON array of multiple API keys
    default_model TEXT NOT NULL DEFAULT '',
    models TEXT NOT NULL DEFAULT '[]', -- JSON array of supported model names
    disabled_models TEXT NOT NULL DEFAULT '[]', -- JSON array of disabled model names
    strategy TEXT NOT NULL DEFAULT 'failsafe', -- failsafe, round-robin, random
    key_strategy TEXT NOT NULL DEFAULT 'round-robin', -- round-robin, random, failover
    proxy_enabled INTEGER NOT NULL DEFAULT 0,
    proxy_group TEXT NOT NULL DEFAULT '',
    is_active INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 10,
    settings_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS model_combos (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    targets_json TEXT NOT NULL DEFAULT '[]', -- [{"provider_id":"openai","model":"gpt-4o","priority":1}, ...]
    strategy TEXT NOT NULL DEFAULT 'failsafe', -- failsafe, round-robin, random
    is_active INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS proxy_nodes (
    id TEXT PRIMARY KEY,
    url TEXT NOT NULL,
    protocol TEXT NOT NULL DEFAULT 'http', -- http, https, socks5
    label TEXT NOT NULL DEFAULT '',
    group_name TEXT NOT NULL DEFAULT 'default',
    is_active INTEGER NOT NULL DEFAULT 1,
    fail_count INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0,
    avg_latency_ms INTEGER NOT NULL DEFAULT 0,
    last_checked DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS channels (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL, -- telegram, whatsapp
    name TEXT NOT NULL,
    identifier TEXT NOT NULL, -- bot token, session id, phone, or bridge webhook
    is_active INTEGER NOT NULL DEFAULT 1,
    default_agent TEXT NOT NULL DEFAULT 'default',
    default_model TEXT NOT NULL DEFAULT '',
    settings_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS channel_tool_perms (
    channel_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    is_allowed INTEGER NOT NULL DEFAULT 1,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (channel_id, tool_name)
);

CREATE TABLE IF NOT EXISTS cron_jobs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    cron_expr TEXT NOT NULL,
    target_channel TEXT NOT NULL,
    target_chat_id TEXT NOT NULL,
    prompt TEXT NOT NULL,
    is_active INTEGER NOT NULL DEFAULT 1,
    last_run DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS chat_sessions (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL,
    chat_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    is_active INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS chat_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL, -- system, user, assistant, tool
    content TEXT NOT NULL,
    tokens INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(session_id) REFERENCES chat_sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_chat_messages_session ON chat_messages(session_id, created_at);

CREATE TABLE IF NOT EXISTS memory_items (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL, -- global, channel, user
    scope_id TEXT NOT NULL,
    key_tag TEXT NOT NULL,
    content TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'fact',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_memory_items_scope ON memory_items(scope, scope_id);

CREATE TABLE IF NOT EXISTS memories (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL DEFAULT 'factual',      -- factual, episodic, procedural, semantic
    scope TEXT NOT NULL DEFAULT 'user',        -- global, channel, user
    scope_id TEXT NOT NULL DEFAULT '',         -- '399999658', channel ID, system
    session_id TEXT NOT NULL DEFAULT '',       -- optional topic ID
    key TEXT NOT NULL,
    content TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'fact',     -- preference, profile, fact, work, decision, sop
    embedding BLOB,                            -- IEEE 754 float32 byte array
    metadata TEXT NOT NULL DEFAULT '{}',       -- JSON string
    is_promoted INTEGER NOT NULL DEFAULT 0,    -- 1 if promoted to permanent long-term memory
    access_count INTEGER NOT NULL DEFAULT 0,
    last_accessed_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_memories_scope_key ON memories(scope, scope_id, key);
CREATE INDEX IF NOT EXISTS idx_memories_scope_type ON memories(scope, scope_id, type);
CREATE INDEX IF NOT EXISTS idx_memories_session ON memories(session_id);
CREATE INDEX IF NOT EXISTS idx_memories_updated ON memories(updated_at DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    id UNINDEXED,
    key,
    content,
    category,
    content=memories,
    content_rowid=rowid
);

CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts(rowid, id, key, content, category)
    VALUES (new.rowid, new.id, new.key, new.content, new.category);
END;

CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, id, key, content, category)
    VALUES('delete', old.rowid, old.id, old.key, old.content, old.category);
END;

CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, id, key, content, category)
    VALUES('delete', old.rowid, old.id, old.key, old.content, old.category);
    INSERT INTO memories_fts(rowid, id, key, content, category)
    VALUES (new.rowid, new.id, new.key, new.content, new.category);
END;

CREATE TABLE IF NOT EXISTS audit_logs (
    id TEXT PRIMARY KEY,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    channel_type TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    chat_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    user_name TEXT NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    tokens_saved INTEGER NOT NULL DEFAULT 0,
    proxy_used TEXT NOT NULL DEFAULT '',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    cost_usd REAL NOT NULL DEFAULT 0.0,
    tools_called TEXT NOT NULL DEFAULT '[]',
    client_request TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL DEFAULT '',
    full_request_payload TEXT NOT NULL DEFAULT '',
    provider_response TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'success',
    error_message TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_time ON audit_logs(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_channel ON audit_logs(channel_id);

CREATE TABLE IF NOT EXISTS response_cache (
    cache_key TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL,
    model TEXT NOT NULL,
    prompt TEXT NOT NULL,
    response_text TEXT NOT NULL,
    thinking_text TEXT NOT NULL DEFAULT '',
    tools_called TEXT NOT NULL DEFAULT '[]',
    media_files TEXT NOT NULL DEFAULT '[]',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    thinking_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd REAL NOT NULL DEFAULT 0.0,
    expires_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_response_cache_expires ON response_cache(expires_at);

CREATE TABLE IF NOT EXISTS model_catalog (
    model_id TEXT PRIMARY KEY,
    raw_id TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    provider_family TEXT NOT NULL DEFAULT '',
    modality TEXT NOT NULL DEFAULT 'text->text',
    supports_vision INTEGER NOT NULL DEFAULT 0,
    supports_audio_in INTEGER NOT NULL DEFAULT 0,
    supports_audio_out INTEGER NOT NULL DEFAULT 0,
    supports_tools INTEGER NOT NULL DEFAULT 1,
    supports_reasoning INTEGER NOT NULL DEFAULT 0,
    context_length INTEGER NOT NULL DEFAULT 4096,
    is_custom INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_model_catalog_vision ON model_catalog(supports_vision);
CREATE INDEX IF NOT EXISTS idx_model_catalog_audio_out ON model_catalog(supports_audio_out);
`
