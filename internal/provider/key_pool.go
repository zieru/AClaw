package provider

import (
	"math/rand"
	"strings"
	"sync"
	"time"
)

// KeyPool manages multiple API keys with rotation and rate-limit cooldown
type KeyPool struct {
	mu           sync.RWMutex
	keys         []string
	currentIndex int
	strategy     string // round-robin, random, failover
	cooldowns    map[string]time.Time
}

// NewKeyPool creates a new key rotation pool
func NewKeyPool(keys []string, strategy string) *KeyPool {
	if strategy == "" {
		strategy = "round-robin"
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
		strategy:  strings.ToLower(strategy),
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

	// If all keys in cooldown, ignore cooldown and use all keys
	if len(available) == 0 {
		available = kp.keys
	}

	switch kp.strategy {
	case "random":
		return available[rand.Intn(len(available))]

	case "failover":
		// Always prefer first available key
		return available[0]

	default: // "round-robin"
		kp.currentIndex = (kp.currentIndex + 1) % len(available)
		return available[kp.currentIndex]
	}
}

// MarkError puts key into cooldown if it was rate limited or had quota errors
func (kp *KeyPool) MarkError(key string, isRateLimit bool) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if isRateLimit {
		// 60 seconds cooldown for 429 rate limits
		kp.cooldowns[key] = time.Now().Add(60 * time.Second)
	} else {
		// 15 seconds cooldown for generic errors
		kp.cooldowns[key] = time.Now().Add(15 * time.Second)
	}
}

// MarkSuccess clears cooldown on successful request
func (kp *KeyPool) MarkSuccess(key string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	delete(kp.cooldowns, key)
}
