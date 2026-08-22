package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
}

var (
	defaultCacheOnce sync.Once
	globalCache      *ResponseCache

	// Time-sensitive pattern detector to bypass cache for dynamic/realtime queries
	timeSensitiveRegex = regexp.MustCompile(`(?i)\b(sekarang|jam berapa|hari ini|tanggal|waktu saat ini|realtime|terbaru|latest|current time|right now|today|what time|whoami)\b`)
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
	defer c.mu.Unlock()

	now := time.Now()
	for k, v := range c.entries {
		if now.After(v.ExpiresAt) {
			delete(c.entries, k)
		}
	}
}

// ComputeKey generates a deterministic sha256 hash
func (c *ResponseCache) ComputeKey(channelID, model, prompt string) string {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	h := sha256.New()
	h.Write([]byte(channelID))
	h.Write([]byte("|"))
	h.Write([]byte(model))
	h.Write([]byte("|"))
	h.Write([]byte(normalized))
	return hex.EncodeToString(h.Sum(nil))
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

// Get retrieves a response from cache if not expired
func (c *ResponseCache) Get(channelID, model, prompt string) (*AgentResponse, bool) {
	if !c.IsEligible(prompt, false) {
		c.missCount.Add(1)
		return nil, false
	}

	key := c.ComputeKey(channelID, model, prompt)

	c.mu.RLock()
	item, found := c.entries[key]
	if !found {
		c.mu.RUnlock()
		c.missCount.Add(1)
		return nil, false
	}

	if time.Now().After(item.ExpiresAt) {
		c.mu.RUnlock()
		// Expired item
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		c.missCount.Add(1)
		return nil, false
	}

	c.mu.RUnlock()

	// Cache Hit!
	c.hitCount.Add(1)
	c.tokensSaved.Add(int64(item.Response.TotalTokens))

	// Return a copy with zero latency to reflect cache delivery
	respCopy := item.Response
	respCopy.Latency = 1 * time.Millisecond
	return &respCopy, true
}

// Set saves a response to cache
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

	c.mu.Lock()
	defer c.mu.Unlock()

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

	c.entries[key] = &CachedResponse{
		Response:  *resp,
		CachedAt:  time.Now(),
		ExpiresAt: time.Now().Add(ttl),
	}
}

// Flush clears all cache entries
func (c *ResponseCache) Flush() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := len(c.entries)
	c.entries = make(map[string]*CachedResponse)
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
