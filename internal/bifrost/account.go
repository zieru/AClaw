package bifrost

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"goassistant/internal/storage"

	"github.com/maximhq/bifrost/core/schemas"
)

// Account implements schemas.Account backed by GoAssistant's SQLite provider
// records. Every active provider record is exposed as a Bifrost custom provider
// keyed by its record Name (or ID when Name is empty). This keeps the DB as the
// single source of truth for providers, keys, models and network config while
// Bifrost owns routing, fallback and load balancing.
type Account struct {
	db *storage.DB
	mu sync.RWMutex
}

// NewAccount creates an Account backed by the given DB.
func NewAccount(db *storage.DB) *Account {
	return &Account{db: db}
}

// providerKey returns the Bifrost provider key for a record.
func providerKey(p *storage.ProviderRecord) schemas.ModelProvider {
	if p != nil && strings.TrimSpace(p.Name) != "" {
		return schemas.ModelProvider(strings.TrimSpace(p.Name))
	}
	if p != nil && strings.TrimSpace(p.ID) != "" {
		return schemas.ModelProvider(strings.TrimSpace(p.ID))
	}
	return schemas.ModelProvider("default")
}

// GetConfiguredProviders returns all active providers as Bifrost provider keys.
func (a *Account) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	recs, err := a.db.ListProviders()
	if err != nil {
		return nil, err
	}
	var out []schemas.ModelProvider
	seen := make(map[string]bool)
	for i := range recs {
		p := &recs[i]
		if !p.IsActive {
			continue
		}
		k := providerKey(p)
		if seen[string(k)] {
			continue
		}
		seen[string(k)] = true
		out = append(out, k)
	}
	return out, nil
}

// findRecord returns the provider record matching a Bifrost provider key.
func (a *Account) findRecord(key string) *storage.ProviderRecord {
	recs, err := a.db.ListProviders()
	if err != nil {
		return nil
	}
	for i := range recs {
		p := &recs[i]
		if strings.EqualFold(string(providerKey(p)), key) || strings.EqualFold(strings.TrimSpace(p.ID), key) {
			return p
		}
	}
	return nil
}

// GetKeysForProvider returns the API keys for the given provider.
func (a *Account) GetKeysForProvider(ctx context.Context, providerKey schemas.ModelProvider) ([]schemas.Key, error) {
	rec := a.findRecord(string(providerKey))
	if rec == nil {
		return nil, fmt.Errorf("provider %s tidak ditemukan", providerKey)
	}

	keys := rec.APIKeys
	if len(keys) == 0 && rec.APIKey != "" {
		keys = []string{rec.APIKey}
	}
	if len(keys) == 0 && !isKeylessType(rec.Type) {
		return nil, nil
	}
	if len(keys) == 0 {
		keys = []string{"no-key"}
	}

	models := rec.EnabledModels()
	if len(models) == 0 && rec.DefaultModel != "" {
		models = []string{rec.DefaultModel}
	}
	if len(models) == 0 {
		models = []string{"*"}
	}

	var out []schemas.Key
	for i, k := range keys {
		out = append(out, schemas.Key{
			Name:   fmt.Sprintf("%s-key-%d", rec.Name, i+1),
			Value:  schemas.SecretVar{Val: k},
			Models: schemas.WhiteList(models),
			Weight: 1.0,
		})
	}
	return out, nil
}

// GetConfigForProvider returns the provider configuration.
func (a *Account) GetConfigForProvider(providerKey schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	rec := a.findRecord(string(providerKey))
	if rec == nil {
		return nil, fmt.Errorf("provider %s tidak ditemukan", providerKey)
	}

	dynCfg := LoadDynamicConfig(a.db)

	nc := schemas.NetworkConfig{
		BaseURL:                        normalizeBaseURL(rec.BaseURL, rec.Type),
		DefaultRequestTimeoutInSeconds: dynCfg.TimeoutSeconds,
		MaxRetries:                     dynCfg.MaxRetries,
		RetryBackoffInitial:            500 * time.Millisecond,
		RetryBackoffMax:                time.Duration(dynCfg.RetryBackoffMaxSec) * time.Second,
		StreamIdleTimeoutInSeconds:     dynCfg.StreamIdleTimeout,
		KeepAliveTimeoutInSeconds:      dynCfg.KeepAliveTimeout,
		InsecureSkipVerify:             dynCfg.InsecureSkipVerify,
		AllowPrivateNetwork:            dynCfg.AllowPrivateNetwork,
	}

	cfg := &schemas.ProviderConfig{
		NetworkConfig: nc,
		// Configurable concurrency and buffer size per provider
		ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{
			Concurrency: dynCfg.Concurrency,
			BufferSize:  dynCfg.BufferSize,
		},
	}

	baseType := mapBaseProviderType(rec.Type)
	cfg.CustomProviderConfig = &schemas.CustomProviderConfig{
		BaseProviderType:      baseType,
		IsKeyLess:             isKeylessType(rec.Type),
		DoesNotSendDoneMarker: dynCfg.DoesNotSendDone,
		WaitForUsage:          dynCfg.WaitForUsage,
	}

	if dynCfg.PromptCache {
		cfg.PromptCache = &schemas.PromptCacheConfig{AutoInject: true}
	}

	proxyTarget := dynCfg.ProxyURL
	if rec.ProxyEnabled && proxyTarget != "" {
		pc, perr := parseProxyURL(proxyTarget)
		if perr == nil {
			cfg.ProxyConfig = pc
		}
	}

	return cfg, nil
}

// normalizeBaseURL removes a trailing /v1 from provider records. Bifrost's
// provider implementations append their own versioned endpoint paths (for
// example /v1/chat/completions); retaining /v1 in the stored base URL causes
// custom OpenAI-compatible endpoints to receive /v1/v1/... and return 404.
func normalizeBaseURL(raw string, provType string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	if strings.EqualFold(provType, "gemini") {
		if base == "" {
			return "https://generativelanguage.googleapis.com/v1beta"
		}
		if !strings.HasSuffix(strings.ToLower(base), "/v1beta") {
			return base + "/v1beta"
		}
		return base
	}
	if strings.HasSuffix(strings.ToLower(base), "/v1") {
		base = strings.TrimRight(base[:len(base)-len("/v1")], "/")
	}
	return base
}

// parseProxyURL converts an "http://user:pass@host:port" or "socks5://host:port"
// string into a Bifrost ProxyConfig.
func parseProxyURL(raw string) (*schemas.ProxyConfig, error) {
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	typ := schemas.HTTPProxy
	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h":
		typ = schemas.Socks5Proxy
	case "http", "https":
		typ = schemas.HTTPProxy
	case "":
		typ = schemas.HTTPProxy
	default:
		return nil, fmt.Errorf("skema proxy tidak didukung: %s", u.Scheme)
	}
	if u.Scheme == "" {
		u.Scheme = "http"
	}
	proxyURL := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
	pc := &schemas.ProxyConfig{
		Type: typ,
		URL:  &schemas.SecretVar{Val: proxyURL},
	}
	if u.User != nil {
		pc.Username = &schemas.SecretVar{Val: u.User.Username()}
		if pw, ok := u.User.Password(); ok {
			pc.Password = &schemas.SecretVar{Val: pw}
		}
	}
	return pc, nil
}

// isKeylessType returns true for provider types that do not require an API key.
func isKeylessType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "free_openai", "free_gemini", "free", "opencodefree", "free_router":
		return true
	default:
		return false
	}
}

// mapBaseProviderType maps a GoAssistant provider type to a Bifrost base provider type.
func mapBaseProviderType(t string) schemas.ModelProvider {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "anthropic":
		return schemas.Anthropic
	case "gemini":
		return schemas.Gemini
	case "ollama":
		return schemas.Ollama
	case "groq":
		return schemas.Groq
	case "deepseek":
		return schemas.DeepSeek
	case "mistral":
		return schemas.Mistral
	case "cohere":
		return schemas.Cohere
	case "cerebras":
		return schemas.Cerebras
	case "perplexity":
		return schemas.Perplexity
	case "openrouter":
		return schemas.OpenRouter
	case "xai", "x-ai":
		return schemas.XAI
	case "sgl", "sglang":
		return schemas.SGL
	case "vllm":
		return schemas.VLLM
	case "vertex":
		return schemas.Vertex
	case "azure":
		return schemas.Azure
	case "bedrock":
		return schemas.Bedrock
	case "replicate":
		return schemas.Replicate
	case "runware":
		return schemas.Runware
	case "runway":
		return schemas.Runway
	case "fireworks":
		return schemas.Fireworks
	case "databricks":
		return schemas.Databricks
	case "huggingface":
		return schemas.HuggingFace
	case "nebius":
		return schemas.Nebius
	case "parasail":
		return schemas.Parasail
	case "wafer":
		return schemas.Wafer
	case "elevenlabs":
		return schemas.Elevenlabs
	case "sarvam":
		return schemas.Sarvam
	case "typesafe":
		return schemas.Typesafe
	case "githubcopilot", "copilot":
		return schemas.GithubCopilot
	default:
		// 9router, custom, dahl, free_*, gemini_web etc. are OpenAI-compatible
		return schemas.OpenAI
	}
}
