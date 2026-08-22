package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResponseCache(t *testing.T) {
	cache := NewResponseCache(100, 1*time.Second)
	defer cache.Close()

	channelID := "test_ch"
	model := "gpt-4o"
	prompt := "Halo apa kabar?"

	// Initially miss
	if _, hit := cache.Get(channelID, model, prompt); hit {
		t.Fatalf("expected cache miss on empty cache")
	}

	// Set response
	resp := &AgentResponse{
		Text:        "Halo! Saya baik-baik saja.",
		TotalTokens: 25,
	}
	cache.Set(channelID, model, prompt, resp, 500*time.Millisecond)

	// Hit
	cached, hit := cache.Get(channelID, model, prompt)
	if !hit || cached == nil {
		t.Fatalf("expected cache hit after Set")
	}
	if cached.Text != resp.Text {
		t.Errorf("expected text %s, got %s", resp.Text, cached.Text)
	}

	// Stats check
	stats := cache.Stats()
	if stats.HitCount != 1 {
		t.Errorf("expected 1 hit, got %d", stats.HitCount)
	}
	if stats.TokensSaved != 25 {
		t.Errorf("expected 25 tokens saved, got %d", stats.TokensSaved)
	}

	// Wait for TTL expiration
	time.Sleep(600 * time.Millisecond)
	if _, hit := cache.Get(channelID, model, prompt); hit {
		t.Errorf("expected cache miss after TTL expiration")
	}

	// Test Eligibility
	if cache.IsEligible("jam berapa sekarang?", false) {
		t.Errorf("expected time-sensitive query to be ineligible for caching")
	}
	if cache.IsEligible("/help", false) {
		t.Errorf("expected slash command to be ineligible for exact response caching")
	}
	if cache.IsEligible("jelaskan konsep arsitektur monolitik", false) != true {
		t.Errorf("expected general conceptual query to be eligible for caching")
	}
}

func TestCommandRouter(t *testing.T) {
	cache := NewResponseCache(10, 1*time.Minute)
	defer cache.Close()
	router := NewCommandRouter(cache)

	reqPing := UserRequest{UserPrompt: "/ping"}
	resp, handled := router.TryHandleLocal(context.Background(), reqPing)
	if !handled || resp == nil {
		t.Fatalf("expected /ping to be handled by CommandRouter")
	}
	if !strings.Contains(resp.Text, "Pong") {
		t.Errorf("expected Pong in response, got %s", resp.Text)
	}

	reqHelp := UserRequest{UserPrompt: "/help"}
	respHelp, handledHelp := router.TryHandleLocal(context.Background(), reqHelp)
	if !handledHelp || respHelp == nil {
		t.Fatalf("expected /help to be handled by CommandRouter")
	}
	if !strings.Contains(respHelp.Text, "Panduan") {
		t.Errorf("expected Panduan in help response, got %s", respHelp.Text)
	}

	reqNormal := UserRequest{UserPrompt: "Bantu saya coding Go"}
	_, handledNormal := router.TryHandleLocal(context.Background(), reqNormal)
	if handledNormal {
		t.Errorf("expected normal user query to NOT be handled by CommandRouter")
	}
}

func TestStaticFirstPromptBuilder(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "md_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	_ = os.WriteFile(filepath.Join(tempDir, "IDENTITY.md"), []byte("# Identity\nGoAssistant Static Identity"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "SOUL.md"), []byte("Core Knowledge Static SOP"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte("Specialized Roles Static Guidelines"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "TOOLS.md"), []byte("Tool Usage Static Guidelines"), 0644)

	mdLoader := NewMDLoader(tempDir)
	pb := NewPromptBuilder(mdLoader)

	sysPrompt, err := pb.BuildSystemPrompt(PromptContext{
		ChannelID:   "test_channel",
		ChannelName: "General",
		ChannelType: "telegram",
		UserName:    "Developer",
		UserID:      "12345",
		ActiveModel: "gpt-4o",
	})
	if err != nil {
		t.Fatalf("failed to build system prompt: %v", err)
	}

	// Verify static components appear BEFORE dynamic timestamp
	idxIdentity := strings.Index(sysPrompt, "GoAssistant Static Identity")
	idxTools := strings.Index(sysPrompt, "Tool Usage Static Guidelines")
	idxTime := strings.Index(sysPrompt, "Current Time:")

	if idxIdentity == -1 || idxTools == -1 || idxTime == -1 {
		t.Fatalf("missing required prompt components in generated prompt")
	}

	if idxTools > idxTime {
		t.Errorf("expected static TOOLS.md (%d) to appear BEFORE dynamic Current Time (%d) for prefix caching", idxTools, idxTime)
	}
	if idxIdentity > idxTools {
		t.Errorf("expected IDENTITY.md (%d) to appear BEFORE TOOLS.md (%d)", idxIdentity, idxTools)
	}
}
