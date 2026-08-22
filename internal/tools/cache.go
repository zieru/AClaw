package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

type CachedToolResult struct {
	Result    string
	CachedAt  time.Time
	ExpiresAt time.Time
}

type ToolCacheStats struct {
	TotalEntries int
	HitCount     int64
	MissCount    int64
}

type ToolResultCache struct {
	mu        sync.RWMutex
	entries   map[string]*CachedToolResult
	hitCount  atomic.Int64
	missCount atomic.Int64
}

var (
	toolCacheOnce sync.Once
	globalToolCache *ToolResultCache
)

func GetGlobalToolCache() *ToolResultCache {
	toolCacheOnce.Do(func() {
		globalToolCache = &ToolResultCache{
			entries: make(map[string]*CachedToolResult),
		}
	})
	return globalToolCache
}

func (c *ToolResultCache) computeKey(toolName string, args map[string]interface{}) string {
	b, _ := json.Marshal(args)
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte("|"))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

func (c *ToolResultCache) Get(toolName string, args map[string]interface{}) (string, bool) {
	key := c.computeKey(toolName, args)
	c.mu.RLock()
	item, ok := c.entries[key]
	if !ok {
		c.mu.RUnlock()
		c.missCount.Add(1)
		return "", false
	}
	if time.Now().After(item.ExpiresAt) {
		c.mu.RUnlock()
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		c.missCount.Add(1)
		return "", false
	}
	c.mu.RUnlock()

	c.hitCount.Add(1)
	return item.Result, true
}

func (c *ToolResultCache) Set(toolName string, args map[string]interface{}, result string, ttl time.Duration) {
	if result == "" || ttl <= 0 {
		return
	}
	key := c.computeKey(toolName, args)
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &CachedToolResult{
		Result:    result,
		CachedAt:  time.Now(),
		ExpiresAt: time.Now().Add(ttl),
	}
}

func (c *ToolResultCache) Flush() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := len(c.entries)
	c.entries = make(map[string]*CachedToolResult)
	return count
}

func (c *ToolResultCache) Stats() ToolCacheStats {
	c.mu.RLock()
	total := len(c.entries)
	c.mu.RUnlock()
	return ToolCacheStats{
		TotalEntries: total,
		HitCount:     c.hitCount.Load(),
		MissCount:    c.missCount.Load(),
	}
}
