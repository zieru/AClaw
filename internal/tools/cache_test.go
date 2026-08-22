package tools

import (
	"testing"
	"time"
)

func TestToolResultCache(t *testing.T) {
	cache := GetGlobalToolCache()
	cache.Flush()

	toolName := "tavily_search"
	args := map[string]interface{}{
		"query": "berita golang terbaru",
	}

	// Miss
	if _, hit := cache.Get(toolName, args); hit {
		t.Fatalf("expected miss on empty tool cache")
	}

	// Set
	resultText := "Hasil pencarian Golang 1.24 release notes"
	cache.Set(toolName, args, resultText, 500*time.Millisecond)

	// Hit
	cached, hit := cache.Get(toolName, args)
	if !hit || cached != resultText {
		t.Fatalf("expected tool cache hit with matching result")
	}

	// Expiry
	time.Sleep(600 * time.Millisecond)
	if _, hit := cache.Get(toolName, args); hit {
		t.Fatalf("expected tool cache miss after TTL expiration")
	}
}
