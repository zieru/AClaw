package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"goassistant/internal/storage"
)

type CachedResponse struct {
	Response  AgentResponse
	ExpiresAt time.Time
	CachedAt  time.Time
	Hits      int
}

type CacheStats struct {
	TotalEntries int
	HitCount     int64
	MissCount    int64
	TokensSaved  int64
	HitRate      float64
}

type ResponseCache struct {
	mu          sync.RWMutex
	entries     map[string]*CachedResponse
	hitCount    atomic.Int64
	missCount   atomic.Int64
	tokensSaved atomic.Int64
	maxEntries  int
	defaultTTL  time.Duration
	stopChan    chan struct{}
	db          *storage.DB
}

var (
	defaultCacheOnce sync.Once
	globalCache      *ResponseCache

	// Time-sensitive pattern detector to bypass cache for dynamic/realtime queries
	timeSensitiveRegex = regexp.MustCompile(`(?i)\b(sekarang|jam berapa|hari ini|tanggal|waktu saat ini|realtime|terbaru|latest|current time|right now|today|what time|whoami)\b`)

	multiSpaceRegex = regexp.MustCompile(`\s+`)
)

func GetGlobalResponseCache() *ResponseCache {
	defaultCacheOnce.Do(func() {
		globalCache = NewResponseCache(5000, 30*time.Minute)
	})
	return globalCache
}

func NewResponseCache(maxEntries int, defaultTTL time.Duration) *ResponseCache {
	if maxEntries <= 0 {
		maxEntries = 5000
	}
	if defaultTTL <= 0 {
		defaultTTL = 30 * time.Minute
	}

	c := &ResponseCache{
		entries:    make(map[string]*CachedResponse),
		maxEntries: maxEntries,
		defaultTTL: defaultTTL,
		stopChan:   make(chan struct{}),
	}

	// Start background cleaner ticker
	go c.startCleaner(5 * time.Minute)

	return c
}

// SetDB links SQLite storage to response cache for persistence across restarts
func (c *ResponseCache) SetDB(db *storage.DB) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.db = db
}

func (c *ResponseCache) Close() {
	close(c.stopChan)
}

func (c *ResponseCache) startCleaner(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			c.cleanupExpired()
		}
	}
}

func (c *ResponseCache) cleanupExpired() {
	c.mu.Lock()
	now := time.Now()
	for k, v := range c.entries {
		if now.After(v.ExpiresAt) {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()

	if c.db != nil {
		_, _ = c.db.DeleteExpiredCachedResponses()
	}
}

// NormalizePrompt cleans punctuation, collapses whitespace, and lowercases text
func NormalizePrompt(prompt string) string {
	s := strings.ToLower(prompt)
	s = multiSpaceRegex.ReplaceAllString(s, " ")
	s = strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	})
	return s
}

// ComputeKey generates a deterministic sha256 hash based on normalized prompt
func (c *ResponseCache) ComputeKey(channelID, model, prompt string) string {
	normalized := NormalizePrompt(prompt)
	h := sha256.New()
	h.Write([]byte(channelID))
	h.Write([]byte("|"))
	h.Write([]byte(model))
	h.Write([]byte("|"))
	h.Write([]byte(normalized))
	return hex.EncodeToString(h.Sum(nil))
}

// ComputeGlobalKey generates a key for cross-channel shared knowledge
func (c *ResponseCache) ComputeGlobalKey(model, prompt string) string {
	return c.ComputeKey("global", model, prompt)
}

// IsEligible checks if a query is safe to cache
func (c *ResponseCache) IsEligible(prompt string, hasAttachment bool) bool {
	if hasAttachment {
		return false
	}
	trimmed := strings.TrimSpace(prompt)
	if len(trimmed) < 2 {
		return false
	}
	// Avoid caching slash commands that might be interactive or dynamic
	if strings.HasPrefix(trimmed, "/") {
		return false
	}
	// Bypass if query is time-sensitive
	if timeSensitiveRegex.MatchString(trimmed) {
		return false
	}
	return true
}

// Get retrieves a response from cache (memory first, then global key, then SQLite fallback)
func (c *ResponseCache) Get(channelID, model, prompt string) (*AgentResponse, bool) {
	if !c.IsEligible(prompt, false) {
		c.missCount.Add(1)
		return nil, false
	}

	key := c.ComputeKey(channelID, model, prompt)
	globalKey := c.ComputeGlobalKey(model, prompt)

	// 1. Check in-memory cache (channel key, then fallback to global key)
	c.mu.RLock()
	item, found := c.entries[key]
	if !found && channelID != "global" {
		item, found = c.entries[globalKey]
	}
	c.mu.RUnlock()

	now := time.Now()
	if found {
		if now.Before(item.ExpiresAt) {
			// Memory Hit!
			c.hitCount.Add(1)
			c.tokensSaved.Add(int64(item.Response.TotalTokens))
			respCopy := item.Response
			respCopy.Latency = 1 * time.Millisecond
			return &respCopy, true
		}
		// Expired item in memory
		c.mu.Lock()
		delete(c.entries, key)
		delete(c.entries, globalKey)
		c.mu.Unlock()
	}

	// 2. Fallback to SQLite DB persistence if configured
	if c.db != nil {
		rec, err := c.db.GetCachedResponse(key)
		if err == nil && rec == nil && channelID != "global" {
			rec, err = c.db.GetCachedResponse(globalKey)
		}
		if err == nil && rec != nil && now.Before(rec.ExpiresAt) {
			// Hydrate AgentResponse
			var tools []string
			_ = json.Unmarshal([]byte(rec.ToolsCalled), &tools)
			var media []MediaAttachment
			_ = json.Unmarshal([]byte(rec.MediaFiles), &media)

			resp := AgentResponse{
				Text:             rec.ResponseText,
				ThinkingContent:  rec.ThinkingText,
				ToolsUsed:        tools,
				MediaFiles:       media,
				PromptTokens:     rec.PromptTokens,
				CompletionTokens: rec.CompletionTokens,
				ThinkingTokens:   rec.ThinkingTokens,
				TotalTokens:      rec.TotalTokens,
				Latency:          1 * time.Millisecond,
			}

			// Re-populate in-memory cache
			c.mu.Lock()
			c.entries[rec.CacheKey] = &CachedResponse{
				Response:  resp,
				CachedAt:  rec.CreatedAt,
				ExpiresAt: rec.ExpiresAt,
			}
			c.mu.Unlock()

			c.hitCount.Add(1)
			c.tokensSaved.Add(int64(resp.TotalTokens))
			return &resp, true
		}
	}

	c.missCount.Add(1)
	return nil, false
}

// Set saves a response to cache (memory and SQLite DB)
func (c *ResponseCache) Set(channelID, model, prompt string, resp *AgentResponse, customTTL time.Duration) {
	if resp == nil || !c.IsEligible(prompt, len(resp.MediaFiles) > 0) {
		return
	}
	// Do not cache error responses or empty texts
	if strings.TrimSpace(resp.Text) == "" || strings.HasPrefix(resp.Text, "⚠️") || strings.HasPrefix(resp.Text, "❌") {
		return
	}

	key := c.ComputeKey(channelID, model, prompt)
	ttl := c.defaultTTL
	if customTTL > 0 {
		ttl = customTTL
	}
	expiresAt := time.Now().Add(ttl)

	c.mu.Lock()
	// If reached maxEntries, evict random or expired
	if len(c.entries) >= c.maxEntries {
		now := time.Now()
		evicted := false
		for k, v := range c.entries {
			if now.After(v.ExpiresAt) {
				delete(c.entries, k)
				evicted = true
				break
			}
		}
		if !evicted {
			// Evict any single element
			for k := range c.entries {
				delete(c.entries, k)
				break
			}
		}
	}

	cachedItem := &CachedResponse{
		Response:  *resp,
		CachedAt:  time.Now(),
		ExpiresAt: expiresAt,
	}
	c.entries[key] = cachedItem

	// If the response didn't call any tools and didn't produce files, it's safe to share globally
	isGlobalEligible := channelID != "global" && len(resp.ToolsUsed) == 0 && len(resp.MediaFiles) == 0
	var globalKey string
	if isGlobalEligible {
		globalKey = c.ComputeGlobalKey(model, prompt)
		c.entries[globalKey] = cachedItem
	}
	c.mu.Unlock()

	// Persist to SQLite DB asynchronously
	if c.db != nil {
		go func(r AgentResponse, k, gKey, chID string, exp time.Time, isGlob bool) {
			toolsJSON, _ := json.Marshal(r.ToolsUsed)
			mediaJSON, _ := json.Marshal(r.MediaFiles)
			rec := storage.ResponseCacheRecord{
				CacheKey:         k,
				ChannelID:        chID,
				Model:            model,
				Prompt:           prompt,
				ResponseText:     r.Text,
				ThinkingText:     r.ThinkingContent,
				ToolsCalled:      string(toolsJSON),
				MediaFiles:       string(mediaJSON),
				PromptTokens:     r.PromptTokens,
				CompletionTokens: r.CompletionTokens,
				ThinkingTokens:   r.ThinkingTokens,
				TotalTokens:      r.TotalTokens,
				CostUSD:          0,
				ExpiresAt:        exp,
			}
			_ = c.db.SetCachedResponse(&rec)
			if isGlob && gKey != "" {
				rec.CacheKey = gKey
				rec.ChannelID = "global"
				_ = c.db.SetCachedResponse(&rec)
			}
		}(*resp, key, globalKey, channelID, expiresAt, isGlobalEligible)
	}
}

// Flush clears all cache entries from memory and SQLite
func (c *ResponseCache) Flush() int {
	c.mu.Lock()
	count := len(c.entries)
	c.entries = make(map[string]*CachedResponse)
	c.mu.Unlock()

	if c.db != nil {
		_ = c.db.FlushCachedResponses()
	}
	return count
}

// Stats returns the current cache performance
func (c *ResponseCache) Stats() CacheStats {
	c.mu.RLock()
	total := len(c.entries)
	c.mu.RUnlock()

	hits := c.hitCount.Load()
	misses := c.missCount.Load()
	totalReqs := hits + misses
	var hitRate float64
	if totalReqs > 0 {
		hitRate = (float64(hits) / float64(totalReqs)) * 100.0
	}

	return CacheStats{
		TotalEntries: total,
		HitCount:     hits,
		MissCount:    misses,
		TokensSaved:  c.tokensSaved.Load(),
		HitRate:      hitRate,
	}
}
