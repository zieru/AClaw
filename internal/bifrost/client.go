package bifrost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/provider"
	"goassistant/internal/storage"

	"github.com/maximhq/bifrost/core/schemas"
)

// Client wraps the embedded Bifrost core instance.
type Client struct {
	mu       sync.RWMutex
	core     *bifrostCore
	account  *Account
	db       *storage.DB
	cfg      config.BifrostConfig
	started  bool
	lastSync map[string]string // provider key -> config hash (for runtime sync)
	stopSync chan struct{}
}

// bifrostCore aliases the Bifrost core package type to keep imports readable.
type bifrostCore = schemasBifrost

var (
	globalClient *Client
	clientOnce   sync.Once
	clientErr    error
)

// GetClient returns the initialized Bifrost client (may be nil when disabled).
func GetClient() *Client {
	if globalClient == nil {
		return nil
	}
	globalClient.mu.RLock()
	defer globalClient.mu.RUnlock()
	if !globalClient.started {
		return nil
	}
	return globalClient
}

// Init initializes the embedded Bifrost core. It returns a non-nil error only
// when Bifrost is enabled in config but fails to initialize.
func Init(db *storage.DB, cfg config.BifrostConfig) (*Client, error) {
	clientOnce.Do(func() {
		if !cfg.Enabled {
			globalClient = nil
			return
		}
		acc := NewAccount(db)
		plugins, perr := buildPlugins(context.Background(), cfg)
		if perr != nil {
			clientErr = fmt.Errorf("bifrost plugins: %w", perr)
			return
		}
		bifrostCfg := schemas.BifrostConfig{Account: acc, LLMPlugins: plugins}
		core, err := initCore(context.Background(), bifrostCfg)
		if err != nil {
			clientErr = fmt.Errorf("bifrost init: %w", err)
			return
		}
		globalClient = newClient(acc, core, db, cfg)
		go globalClient.syncLoop()
	})
	if clientErr != nil {
		return nil, clientErr
	}
	return GetClient(), nil
}

// newClient builds a Client (used by both Init and tests).
func newClient(acc *Account, core *bifrostCore, db *storage.DB, cfg config.BifrostConfig) *Client {
	c := &Client{
		core:     core,
		account:  acc,
		db:       db,
		cfg:      cfg,
		started:  true,
		lastSync: make(map[string]string),
		stopSync: make(chan struct{}),
	}
	return c
}

// Shutdown stops the embedded Bifrost core.
func (c *Client) Shutdown() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.core != nil && c.started {
		if c.stopSync != nil {
			close(c.stopSync)
			c.stopSync = nil
		}
		c.core.Shutdown()
		c.started = false
	}
}

// Enabled reports whether Bifrost is active.
func (c *Client) Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.started
}

// Enabled reports whether the Bifrost gateway is active (package-level helper).
func Enabled() bool {
	c := GetClient()
	return c != nil && c.Enabled()
}

// Core returns the underlying Bifrost instance (used by admin/observability).
func (c *Client) Core() *bifrostCore {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.core
}

// syncLoop periodically reconciles Bifrost providers with the DB.
func (c *Client) syncLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopSync:
			return
		case <-ticker.C:
			_ = c.SyncProviders()
		}
	}
}

// SyncProviders reconciles the runtime Bifrost provider set with the DB. Added
// or changed providers call UpdateProvider; removed providers are dropped.
func (c *Client) SyncProviders() error {
	if c == nil || c.core == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	recs, err := c.db.ListProviders()
	if err != nil {
		return err
	}
	current := make(map[string]string)
	for i := range recs {
		p := &recs[i]
		if !p.IsActive {
			continue
		}
		current[string(providerKey(p))] = providerHash(p)
	}

	var firstErr error
	for key, h := range current {
		if c.lastSync[key] != h {
			if err := c.core.UpdateProvider(schemas.ModelProvider(key)); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			c.lastSync[key] = h
		}
	}
	for key := range c.lastSync {
		if _, ok := current[key]; !ok {
			_ = c.core.RemoveProvider(schemas.ModelProvider(key))
			delete(c.lastSync, key)
		}
	}
	return firstErr
}

// SyncProviderNow forces an immediate re-sync for a single provider (used by
// admin commands that mutate a provider and want the change applied instantly).
func (c *Client) SyncProviderNow(nameOrID string) error {
	if c == nil || c.core == nil {
		return nil
	}
	key := strings.TrimSpace(nameOrID)
	if key == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	providerRecord := c.findProviderByName(key)
	if providerRecord == nil {
		providerRecord = c.findProviderByID(key)
	}
	if providerRecord == nil {
		return fmt.Errorf("provider %q tidak ditemukan", key)
	}
	key = string(providerKey(providerRecord))
	if err := c.core.UpdateProvider(schemas.ModelProvider(key)); err != nil {
		return err
	}
	if h, ok := c.hashForKey(key); ok {
		c.lastSync[key] = h
	}
	return nil
}

func (c *Client) hashForKey(key string) (string, bool) {
	recs, err := c.db.ListProviders()
	if err != nil {
		return "", false
	}
	for i := range recs {
		p := &recs[i]
		if strings.EqualFold(string(providerKey(p)), key) {
			return providerHash(p), true
		}
	}
	return "", false
}

// providerHash returns a stable hash of the fields that affect Bifrost routing.
func providerHash(p *storage.ProviderRecord) string {
	keys := p.APIKeys
	if len(keys) == 0 && p.APIKey != "" {
		keys = []string{p.APIKey}
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s|%t|%s|%d",
		p.Type, p.BaseURL, p.DefaultModel, strings.Join(p.EnabledModels(), ","),
		strings.Join(keys, ","), p.ProxyEnabled, p.ProxyGroup, p.Priority)
}

// ReloadAllProviders clears the sync cache and forces Bifrost to update every active provider.
// This is used when global Bifrost settings (timeouts, concurrency, retries, etc.) change.
func (c *Client) ReloadAllProviders() error {
	if c == nil || c.core == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	recs, err := c.db.ListProviders()
	if err != nil {
		return err
	}
	var firstErr error
	for i := range recs {
		p := &recs[i]
		if !p.IsActive {
			continue
		}
		key := providerKey(p)
		if err := c.core.UpdateProvider(key); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		c.lastSync[string(key)] = providerHash(p)
	}
	return firstErr
}

// SyncProviders forces an immediate full reconciliation (called after admin
// provider mutations so Bifrost picks them up without waiting for the loop).
func SyncProviders() {
	c := GetClient()
	if c == nil {
		return
	}
	_ = c.SyncProviders()
}

// ReloadAll forces all providers in Bifrost to re-read account configuration.
func ReloadAll() error {
	c := GetClient()
	if c == nil {
		return nil
	}
	return c.ReloadAllProviders()
}

// Generate routes a GoAssistant chat request through Bifrost. It implements the
// provider.Router interface, taking over provider selection, fallback chains and
// streaming from the legacy GoAssistant provider manager.
func (c *Client) Generate(ctx context.Context, preferredName string, req provider.ChatRequest) (*provider.ChatResponse, error) {
	if c == nil || c.core == nil {
		return nil, errors.New("bifrost gateway tidak aktif")
	}

	provKey, model, fallbacks := c.resolve(preferredName, req)

	// If resolved provider is a native scraper (gemini_web, gemini_scrape), delegate directly to Manager
	pRec := c.findProviderByName(string(provKey))
	if pRec == nil {
		pRec = c.findProviderByID(string(provKey))
	}
	if pRec != nil && (pRec.Type == "gemini_web" || pRec.Type == "gemini_scrape") {
		if pMgr := provider.GetManager(); pMgr != nil {
			if p, ok := pMgr.Get(string(providerKey(pRec))); ok && p != nil {
				req.Model = model
				return p.GenerateChat(ctx, req)
			}
			if p, ok := pMgr.Get(pRec.ID); ok && p != nil {
				req.Model = model
				return p.GenerateChat(ctx, req)
			}
		}
	}

	bfReq := &schemas.BifrostChatRequest{
		Provider:  schemas.ModelProvider(provKey),
		Model:     model,
		Input:     toBifrostMessages(req.Messages),
		Fallbacks: fallbacks,
	}

	if len(req.Tools) > 0 {
		bfReq.Params = &schemas.ChatParameters{Tools: toBifrostTools(req.Tools)}
	} else {
		bfReq.Params = &schemas.ChatParameters{}
	}
	if req.Temperature > 0 {
		t := req.Temperature
		bfReq.Params.Temperature = &t
	}
	if req.MaxTokens > 0 {
		m := req.MaxTokens
		bfReq.Params.MaxCompletionTokens = &m
	}
	if req.ThinkingEnabled || req.ThinkingLevel != "" && req.ThinkingLevel != "disabled" {
		bfReq.Params.Reasoning = &schemas.ChatReasoning{Effort: schemas.Ptr("high")}
	}

	bifrostCtx := schemas.NewBifrostContext(ctx, schemas.NoDeadline)
	if req.CacheKey != "" {
		setCacheKey(bifrostCtx, req.CacheKey)
	}

	if req.Stream && req.StreamCallback != nil {
		return c.stream(bifrostCtx, bfReq, req)
	}

	resp, berr := c.core.ChatCompletionRequest(bifrostCtx, bfReq)
	if berr != nil {
		return nil, bifrostToErr(berr)
	}
	out := respToChatResponse(resp, req)
	out.Tries = 1
	return out, nil
}

// stream handles a streaming chat request through Bifrost.
func (c *Client) stream(ctx *schemas.BifrostContext, bfReq *schemas.BifrostChatRequest, req provider.ChatRequest) (*provider.ChatResponse, error) {
	chunkCh, berr := c.core.ChatCompletionStreamRequest(ctx, bfReq)
	if berr != nil {
		return nil, bifrostToErr(berr)
	}

	out := &provider.ChatResponse{}
	var rawArgs map[int]*strings.Builder
	for {
		select {
		case <-ctx.Done():
			req.StreamCallback(provider.StreamChunk{Done: true})
			return out, ctx.Err()
		case chunk, ok := <-chunkCh:
			if !ok {
				req.StreamCallback(provider.StreamChunk{Done: true})
				finalizeToolCalls(out, rawArgs)
				out.Tries = 1
				return out, nil
			}
			if chunk.BifrostError != nil {
				req.StreamCallback(provider.StreamChunk{Done: true})
				finalizeToolCalls(out, rawArgs)
				return out, bifrostToErr(chunk.BifrostError)
			}
			if chunk.BifrostChatResponse == nil {
				continue
			}
			r := chunk.BifrostChatResponse
			if r.Usage != nil {
				mergeUsage(out, r.Usage)
			}
			ef := r.ExtraFields
			if ef.RoutingInfo.Provider != "" {
				out.ProviderName = string(ef.RoutingInfo.Provider)
			}
			if ef.RoutingInfo.Model != "" {
				out.Model = ef.RoutingInfo.Model
			}
			if len(r.Choices) > 0 {
				ch := r.Choices[0]
				if ch.ChatStreamResponseChoice != nil && ch.ChatStreamResponseChoice.Delta != nil {
					d := ch.ChatStreamResponseChoice.Delta
					if d.Content != nil {
						out.Content += *d.Content
						req.StreamCallback(provider.StreamChunk{Content: *d.Content})
					}
					if d.Reasoning != nil {
						out.Thinking += *d.Reasoning
						req.StreamCallback(provider.StreamChunk{Thinking: *d.Reasoning})
					}
					mergeToolCallDelta(out, d, &rawArgs)
				}
			}
		}
	}
}

// resolve determines the Bifrost provider key, model and fallbacks for a request.
func (c *Client) resolve(preferredName string, req provider.ChatRequest) (string, string, []schemas.Fallback) {
	provName := strings.TrimSpace(preferredName)
	model := strings.TrimSpace(req.Model)
	preferredModel := strings.TrimSpace(req.PreferredModel)

	// Combo resolution: "combo:<name>" or a bare combo name.
	comboName := ""
	lowerProv := strings.ToLower(provName)
	lowerModel := strings.ToLower(model)
	switch {
	case strings.HasPrefix(lowerModel, "combo:"):
		comboName = strings.TrimPrefix(model, "combo:")
	case strings.HasPrefix(lowerProv, "combo:"):
		comboName = strings.TrimPrefix(provName, "combo:")
	case lowerProv == "auto" || lowerProv == "combo" || lowerProv == "":
		if strings.HasPrefix(lowerModel, "combo:") {
			comboName = strings.TrimPrefix(model, "combo:")
		}
	default:
		if rec := c.findProviderByName(provName); rec == nil {
			if combo, _ := c.db.GetCombo(provName); combo != nil {
				comboName = provName
				provName = ""
			}
		}
	}

	if comboName != "" {
		if combo, _ := c.db.GetCombo(comboName); combo != nil && combo.IsActive && len(combo.Targets) > 0 {
			// Primary = first active target, remaining targets become fallbacks.
			targets := combo.Targets
			primary := targets[0]
			provName = primary.ProviderID
			if p := c.findProviderByID(primary.ProviderID); p != nil {
				provName = string(providerKey(p))
			}
			model = primary.Model
			if len(targets) > 1 {
				fallbacks := make([]schemas.Fallback, 0, len(targets)-1)
				for _, t := range targets[1:] {
					providerName := t.ProviderID
					if p := c.findProviderByID(t.ProviderID); p != nil {
						providerName = string(providerKey(p))
					}
					fallbacks = append(fallbacks, schemas.Fallback{
						Provider: schemas.ModelProvider(providerName),
						Model:    t.Model,
					})
				}
				return provName, model, fallbacks
			}
			return provName, model, nil
		}
	}

	// Parse provider:/resilient: prefixes and provider:model bindings.
	if model != "" && !strings.HasPrefix(lowerModel, "combo:") {
		switch {
		case strings.HasPrefix(lowerModel, "provider:") || strings.HasPrefix(lowerModel, "resilient:"):
			colon := strings.Index(model, ":")
			provName = strings.TrimSpace(model[colon+1:])
			model = ""
		case strings.Contains(model, ":"):
			parts := strings.SplitN(model, ":", 2)
			if rec := c.findProviderByName(parts[0]); rec != nil {
				if provName == "" {
					provName = parts[0]
				}
				model = parts[1]
			}
		}
	}

	// Preferred model fallback (e.g. combo PreferredIfAvailable).
	if model == "" && preferredModel != "" && !strings.HasPrefix(strings.ToLower(preferredModel), "combo:") {
		model = preferredModel
	}

	// Fall back to the configured default provider/model.
	rec := c.findProviderByName(provName)
	if rec == nil && provName != "" {
		// Unknown provider name; try as a type.
		rec = c.findProviderByType(provName)
		if rec != nil {
			provName = string(providerKey(rec))
		}
	}
	// If no provider was requested explicitly, find one that serves the model.
	if rec == nil && model != "" {
		if r := c.findProviderForModel(model); r != nil {
			rec = r
			provName = string(providerKey(rec))
		}
	}
	if rec == nil {
		if d := config.Get(); d != nil {
			rec = c.findProviderByName(d.Defaults.DefaultProvider)
			if rec == nil {
				rec = c.findProviderByType(d.Defaults.DefaultProvider)
			}
			if rec != nil {
				provName = string(providerKey(rec))
				if model == "" {
					model = d.Defaults.DefaultModel
				}
			}
		}
	}
	// Last resort: any active provider.
	if rec == nil {
		recs, err := c.db.ListProviders()
		if err == nil {
			for i := range recs {
				if recs[i].IsActive {
					rec = &recs[i]
					provName = string(providerKey(rec))
					break
				}
			}
		}
	}
	if model == "" && rec != nil {
		model = rec.DefaultModel
	}

	return provName, model, nil
}

// findProviderForModel returns the first active provider whose enabled models
// include the given model (matching default model too).
func (c *Client) findProviderForModel(model string) *storage.ProviderRecord {
	if model == "" {
		return nil
	}
	recs, err := c.db.ListProviders()
	if err != nil {
		return nil
	}
	for i := range recs {
		p := &recs[i]
		if !p.IsActive {
			continue
		}
		if strings.EqualFold(p.DefaultModel, model) {
			return p
		}
		for _, m := range p.EnabledModels() {
			if strings.EqualFold(m, model) {
				return p
			}
		}
	}
	return nil
}

func (c *Client) findProviderByName(name string) *storage.ProviderRecord {
	if name == "" {
		return nil
	}
	recs, err := c.db.ListProviders()
	if err != nil {
		return nil
	}
	for i := range recs {
		if recs[i].IsActive && (strings.EqualFold(strings.TrimSpace(recs[i].Name), strings.TrimSpace(name)) || strings.EqualFold(strings.TrimSpace(recs[i].ID), strings.TrimSpace(name))) {
			return &recs[i]
		}
	}
	return nil
}

func (c *Client) findProviderByType(t string) *storage.ProviderRecord {
	if t == "" {
		return nil
	}
	recs, err := c.db.ListProviders()
	if err != nil {
		return nil
	}
	for i := range recs {
		if recs[i].IsActive && strings.EqualFold(strings.TrimSpace(recs[i].Type), strings.TrimSpace(t)) {
			return &recs[i]
		}
	}
	return nil
}

func (c *Client) findProviderByID(id string) *storage.ProviderRecord {
	if id == "" {
		return nil
	}
	recs, err := c.db.ListProviders()
	if err != nil {
		return nil
	}
	for i := range recs {
		if recs[i].IsActive && strings.EqualFold(strings.TrimSpace(recs[i].ID), strings.TrimSpace(id)) {
			return &recs[i]
		}
	}
	return nil
}

// mergeUsage merges Bifrost usage counters into the response.
func mergeUsage(out *provider.ChatResponse, u *schemas.BifrostLLMUsage) {
	if u == nil {
		return
	}
	if u.PromptTokens > 0 {
		out.PromptTokens = u.PromptTokens
	}
	if u.CompletionTokens > 0 {
		out.CompletionTokens = u.CompletionTokens
	}
	if u.TotalTokens > 0 {
		out.TotalTokens = u.TotalTokens
	}
	if u.Cost != nil {
		out.CostUSD = u.Cost.TotalCost
	}
	if u.PromptTokensDetails != nil {
		if u.PromptTokensDetails.CachedReadTokens > 0 {
			out.CacheReadTokens = u.PromptTokensDetails.CachedReadTokens
		}
		if u.PromptTokensDetails.CachedWriteTokens > 0 {
			out.CacheCreationTokens = u.PromptTokensDetails.CachedWriteTokens
		}
	}
}

// respToChatResponse converts a Bifrost chat response to a GoAssistant response.
func respToChatResponse(resp *schemas.BifrostChatResponse, req provider.ChatRequest) *provider.ChatResponse {
	out := &provider.ChatResponse{}
	if resp == nil {
		return out
	}
	if len(resp.Choices) > 0 {
		ch := resp.Choices[0]
		if ch.ChatNonStreamResponseChoice != nil && ch.ChatNonStreamResponseChoice.Message != nil {
			m := ch.ChatNonStreamResponseChoice.Message
			pm := fromBifrostMessage(*m)
			out.Content = pm.Content
			out.Thinking = thinkingFromMessage(m)
			out.ToolCalls = pm.ToolCalls
		} else if ch.ChatStreamResponseChoice != nil && ch.ChatStreamResponseChoice.Delta != nil {
			d := ch.ChatStreamResponseChoice.Delta
			if d.Content != nil {
				out.Content = *d.Content
			}
			if d.Reasoning != nil {
				out.Thinking = *d.Reasoning
			}
			var rawArgs map[int]*strings.Builder
			mergeToolCallDelta(out, d, &rawArgs)
			finalizeToolCalls(out, rawArgs)
		}
	}
	if resp.Usage != nil {
		mergeUsage(out, resp.Usage)
	}
	ef := resp.ExtraFields
	out.ProviderName = string(ef.RoutingInfo.Provider)
	if ef.RoutingInfo.Model != "" {
		out.Model = ef.RoutingInfo.Model
	} else if resp.Model != "" {
		out.Model = resp.Model
	}
	if ef.Latency > 0 {
		out.Latency = time.Duration(ef.Latency) * time.Millisecond
	}
	return out
}

// bifrostToErr converts a BifrostError to a Go error.
func bifrostToErr(berr *schemas.BifrostError) error {
	if berr == nil {
		return errors.New("bifrost: unknown error")
	}
	if berr.Error != nil && berr.Error.Message != "" {
		return errors.New(berr.Error.Message)
	}
	if berr.StatusCode != nil {
		return fmt.Errorf("bifrost: status %d", *berr.StatusCode)
	}
	return errors.New("bifrost: request failed")
}
