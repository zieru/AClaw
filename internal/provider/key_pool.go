package provider

import (
	"math/rand"
	"strings"
	"sync"
	"time"
)

// KeyPool manages multiple API keys with rotation, strategies (round-robin, failover/fallback, random), and cooldown
type KeyPool struct {
	mu           sync.RWMutex
	keys         []string
	currentIndex int
	strategy     string // round-robin, failover, fallback, random
	cooldowns    map[string]time.Time
}

// NewKeyPool creates a new key rotation pool
func NewKeyPool(keys []string, strategy string) *KeyPool {
	if strategy == "" {
		strategy = "round-robin"
	}
	strat := strings.ToLower(strategy)
	if strat == "fallback" {
		strat = "failover"
	}

	var cleanKeys []string
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" {
			cleanKeys = append(cleanKeys, k)
		}
	}
	return &KeyPool{
		keys:      cleanKeys,
		strategy:  strat,
		cooldowns: make(map[string]time.Time),
	}
}

// SetKeys replaces all keys in the pool
func (kp *KeyPool) SetKeys(keys []string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	var cleanKeys []string
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" {
			cleanKeys = append(cleanKeys, k)
		}
	}
	kp.keys = cleanKeys
	kp.currentIndex = 0
}

// SetStrategy changes the key rotation strategy
func (kp *KeyPool) SetStrategy(strategy string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	strat := strings.ToLower(strings.TrimSpace(strategy))
	if strat == "fallback" {
		strat = "failover"
	}
	if strat != "" {
		kp.strategy = strat
	}
}

// GetStrategy returns the active rotation strategy
func (kp *KeyPool) GetStrategy() string {
	kp.mu.RLock()
	defer kp.mu.RUnlock()
	return kp.strategy
}

// AddKey adds a key to the pool if not already present
func (kp *KeyPool) AddKey(key string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	for _, k := range kp.keys {
		if k == key {
			return
		}
	}
	kp.keys = append(kp.keys, key)
}

// RemoveKey removes a key from the pool
func (kp *KeyPool) RemoveKey(key string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	key = strings.TrimSpace(key)
	var updated []string
	for _, k := range kp.keys {
		if k != key {
			updated = append(updated, k)
		}
	}
	kp.keys = updated
	delete(kp.cooldowns, key)
}

// GetKeys returns a copy of all keys
func (kp *KeyPool) GetKeys() []string {
	kp.mu.RLock()
	defer kp.mu.RUnlock()

	res := make([]string, len(kp.keys))
	copy(res, kp.keys)
	return res
}

// Count returns number of registered keys
func (kp *KeyPool) Count() int {
	kp.mu.RLock()
	defer kp.mu.RUnlock()
	return len(kp.keys)
}

// AvailableCount returns the number of keys not currently in cooldown
func (kp *KeyPool) AvailableCount() int {
	kp.mu.RLock()
	defer kp.mu.RUnlock()

	now := time.Now()
	count := 0
	for _, k := range kp.keys {
		if until, inCooldown := kp.cooldowns[k]; !inCooldown || now.After(until) {
			count++
		}
	}
	return count
}

// GetNextKey selects a key taking cooldowns and strategy into account
func (kp *KeyPool) GetNextKey() string {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if len(kp.keys) == 0 {
		return ""
	}

	now := time.Now()

	// Filter available keys not in active cooldown
	var available []string
	for _, k := range kp.keys {
		if until, inCooldown := kp.cooldowns[k]; !inCooldown || now.After(until) {
			available = append(available, k)
		}
	}

	// If all keys are in cooldown, pick the key with the earliest expiring cooldown
	if len(available) == 0 {
		var earliestKey string
		var earliestTime time.Time
		for _, k := range kp.keys {
			until := kp.cooldowns[k]
			if earliestKey == "" || until.Before(earliestTime) {
				earliestKey = k
				earliestTime = until
			}
		}
		if earliestKey != "" {
			return earliestKey
		}
		available = kp.keys
	}

	switch kp.strategy {
	case "random":
		return available[rand.Intn(len(available))]

	case "failover", "fallback":
		// Failover/fallback strategy: always prioritize the highest-priority (first) available key
		return available[0]

	default: // "round-robin"
		kp.currentIndex = (kp.currentIndex + 1) % len(available)
		return available[kp.currentIndex]
	}
}

// MarkTimeout marks key with a temporary cooldown due to timeout or network failure
func (kp *KeyPool) MarkTimeout(key string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	kp.cooldowns[key] = time.Now().Add(30 * time.Second)
}

// MarkRateLimit marks key with a cooldown due to 429 rate limit or quota exhaustion
func (kp *KeyPool) MarkRateLimit(key string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	kp.cooldowns[key] = time.Now().Add(60 * time.Second)
}

// MarkAuthError marks key with a longer cooldown due to 401 Unauthorized / 403 Forbidden
func (kp *KeyPool) MarkAuthError(key string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	kp.cooldowns[key] = time.Now().Add(5 * time.Minute)
}

// MarkError puts key into cooldown based on error classification (backwards compatible)
func (kp *KeyPool) MarkError(key string, isRateLimit bool) {
	if isRateLimit {
		kp.MarkRateLimit(key)
	} else {
		kp.MarkTimeout(key)
	}
}

// MarkSuccess clears cooldown on successful request
func (kp *KeyPool) MarkSuccess(key string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	delete(kp.cooldowns, key)
}
