package bifrost

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/storage"
)

const (
	SettingConcurrency        = "bifrost_concurrency"
	SettingBufferSize         = "bifrost_buffer_size"
	SettingTimeout            = "bifrost_timeout"
	SettingMaxRetries          = "bifrost_max_retries"
	SettingRetryBackoffMax    = "bifrost_retry_backoff_max"
	SettingStreamIdleTimeout  = "bifrost_stream_idle_timeout"
	SettingKeepAliveTimeout   = "bifrost_keepalive_timeout"
	SettingInsecureSkipVerify = "bifrost_insecure_skip_verify"
	SettingAllowPrivate       = "bifrost_allow_private_network"
	SettingPromptCache        = "bifrost_prompt_cache"
	SettingPromptCacheTTL     = "bifrost_prompt_cache_ttl"
	SettingWaitForUsage       = "bifrost_wait_for_usage"
	SettingNoDoneMarker       = "bifrost_no_done_marker"
	SettingProxyURL           = "bifrost_proxy_url"
	SettingDebugRawPayload    = "bifrost_debug_raw_payload"
)

// DynamicConfig holds configurable Bifrost parameters loaded from DB or config fallback.
type DynamicConfig struct {
	Concurrency         int           `json:"concurrency"`
	BufferSize          int           `json:"buffer_size"`
	TimeoutSeconds      int           `json:"timeout_seconds"`
	MaxRetries          int           `json:"max_retries"`
	RetryBackoffMaxSec  int           `json:"retry_backoff_max_sec"`
	StreamIdleTimeout   int           `json:"stream_idle_timeout"`
	KeepAliveTimeout    int           `json:"keep_alive_timeout"`
	InsecureSkipVerify  bool          `json:"insecure_skip_verify"`
	AllowPrivateNetwork bool          `json:"allow_private_network"`
	PromptCache         bool          `json:"prompt_cache"`
	PromptCacheTTL      string        `json:"prompt_cache_ttl"`
	WaitForUsage        bool          `json:"wait_for_usage"`
	DoesNotSendDone     bool          `json:"does_not_send_done_marker"`
	ProxyURL            string        `json:"proxy_url"`
	DebugRawPayload     bool          `json:"debug_raw_payload"`
}

// DefaultDynamicConfig returns production defaults for GoAssistant on a 1-core VPS.
func DefaultDynamicConfig() DynamicConfig {
	cfg := DynamicConfig{
		Concurrency:         8,
		BufferSize:          64,
		TimeoutSeconds:      90,
		MaxRetries:          2,
		RetryBackoffMaxSec:  120,
		StreamIdleTimeout:   60,
		KeepAliveTimeout:    30,
		InsecureSkipVerify:  false,
		AllowPrivateNetwork: true,
		PromptCache:         true,
		PromptCacheTTL:      "5m",
		WaitForUsage:        true,
		DoesNotSendDone:     false,
		ProxyURL:            "",
		DebugRawPayload:     false,
	}
	if appCfg := config.Get(); appCfg != nil {
		if appCfg.Timeouts.APICallSeconds > 0 {
			cfg.TimeoutSeconds = appCfg.Timeouts.APICallSeconds
		}
		if appCfg.Timeouts.RetrySeconds > 0 {
			cfg.RetryBackoffMaxSec = appCfg.Timeouts.RetrySeconds
		}
		if appCfg.Bifrost.ProxyURL != "" {
			cfg.ProxyURL = appCfg.Bifrost.ProxyURL
		}
		if appCfg.Bifrost.PromptCache {
			cfg.PromptCache = true
		}
	}
	return cfg
}

// LoadDynamicConfig reads Bifrost configuration from system_settings table, falling back to defaults.
func LoadDynamicConfig(db *storage.DB) DynamicConfig {
	cfg := DefaultDynamicConfig()
	if db == nil {
		return cfg
	}

	if v, err := db.GetSetting(SettingConcurrency, ""); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Concurrency = n
		}
	}
	if v, err := db.GetSetting(SettingBufferSize, ""); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.BufferSize = n
		}
	}
	if v, err := db.GetSetting(SettingTimeout, ""); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.TimeoutSeconds = n
		}
	}
	if v, err := db.GetSetting(SettingMaxRetries, ""); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.MaxRetries = n
		}
	}
	if v, err := db.GetSetting(SettingRetryBackoffMax, ""); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.RetryBackoffMaxSec = n
		}
	}
	if v, err := db.GetSetting(SettingStreamIdleTimeout, ""); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.StreamIdleTimeout = n
		}
	}
	if v, err := db.GetSetting(SettingKeepAliveTimeout, ""); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.KeepAliveTimeout = n
		}
	}
	if v, err := db.GetSetting(SettingInsecureSkipVerify, ""); err == nil && v != "" {
		cfg.InsecureSkipVerify = v == "1" || v == "true"
	}
	if v, err := db.GetSetting(SettingAllowPrivate, ""); err == nil && v != "" {
		cfg.AllowPrivateNetwork = v == "1" || v == "true"
	}
	if v, err := db.GetSetting(SettingPromptCache, ""); err == nil && v != "" {
		cfg.PromptCache = v == "1" || v == "true"
	}
	if v, err := db.GetSetting(SettingPromptCacheTTL, ""); err == nil && v != "" {
		cfg.PromptCacheTTL = v
	}
	if v, err := db.GetSetting(SettingWaitForUsage, ""); err == nil && v != "" {
		cfg.WaitForUsage = v == "1" || v == "true"
	}
	if v, err := db.GetSetting(SettingNoDoneMarker, ""); err == nil && v != "" {
		cfg.DoesNotSendDone = v == "1" || v == "true"
	}
	if v, err := db.GetSetting(SettingProxyURL, ""); err == nil && v != "" {
		cfg.ProxyURL = v
	}
	if v, err := db.GetSetting(SettingDebugRawPayload, ""); err == nil && v != "" {
		cfg.DebugRawPayload = v == "1" || v == "true"
	}

	return cfg
}

// SaveDynamicConfig writes setting keys to system_settings in db.
func SaveDynamicConfig(db *storage.DB, cfg DynamicConfig) error {
	if db == nil {
		return fmt.Errorf("database nil")
	}
	m := map[string]string{
		SettingConcurrency:        strconv.Itoa(cfg.Concurrency),
		SettingBufferSize:         strconv.Itoa(cfg.BufferSize),
		SettingTimeout:            strconv.Itoa(cfg.TimeoutSeconds),
		SettingMaxRetries:          strconv.Itoa(cfg.MaxRetries),
		SettingRetryBackoffMax:    strconv.Itoa(cfg.RetryBackoffMaxSec),
		SettingStreamIdleTimeout:  strconv.Itoa(cfg.StreamIdleTimeout),
		SettingKeepAliveTimeout:   strconv.Itoa(cfg.KeepAliveTimeout),
		SettingInsecureSkipVerify: strconv.FormatBool(cfg.InsecureSkipVerify),
		SettingAllowPrivate:       strconv.FormatBool(cfg.AllowPrivateNetwork),
		SettingPromptCache:        strconv.FormatBool(cfg.PromptCache),
		SettingPromptCacheTTL:     cfg.PromptCacheTTL,
		SettingWaitForUsage:       strconv.FormatBool(cfg.WaitForUsage),
		SettingNoDoneMarker:       strconv.FormatBool(cfg.DoesNotSendDone),
		SettingProxyURL:           cfg.ProxyURL,
		SettingDebugRawPayload:    strconv.FormatBool(cfg.DebugRawPayload),
	}
	for k, v := range m {
		if err := db.SetSetting(k, v); err != nil {
			return err
		}
	}
	return nil
}

// ConfigHash generates a stable representation of the current configuration.
func (c DynamicConfig) ConfigHash() string {
	b, _ := json.Marshal(c)
	return string(b)
}

// DurationTTL parses the PromptCacheTTL string or returns 5 minutes.
func (c DynamicConfig) DurationTTL() time.Duration {
	d, err := time.ParseDuration(c.PromptCacheTTL)
	if err != nil || d <= 0 {
		return 5 * time.Minute
	}
	return d
}
