package bifrost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goassistant/internal/config"
	"goassistant/internal/provider"
	"goassistant/internal/storage"

	"github.com/maximhq/bifrost/core/schemas"
)

// newTestClient inits a Bifrost client backed by an in-memory DB with a single
// custom OpenAI-compatible provider pointing at the given base URL.
func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	db, err := storage.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	rec := &storage.ProviderRecord{
		ID:           "mock",
		Name:         "mock",
		Type:         "custom",
		BaseURL:      baseURL,
		APIKey:       "dummy",
		APIKeys:      []string{"dummy"},
		DefaultModel: "test-model",
		Models:       []string{"test-model", "other"},
		IsActive:     true,
	}
	if err := db.SaveProvider(rec); err != nil {
		t.Fatalf("save provider: %v", err)
	}

	acc := NewAccount(db)
	core, err := initCore(context.Background(), schemas.BifrostConfig{Account: acc})
	if err != nil {
		t.Fatalf("bifrost core init: %v", err)
	}
	client := newClient(acc, core, db, config.BifrostConfig{Enabled: true})
	t.Cleanup(func() {
		client.Shutdown()
	})
	return client
}

func TestGenerateNonStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-1","object":"chat.completion","created":0,"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"Halo dari mock!"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)

	resp, err := c.Generate(context.Background(), "", provider.ChatRequest{
		Model:     "test-model",
		MaxTokens: 100,
		Messages: []provider.ChatMessage{
			{Role: provider.RoleUser, Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(resp.Content, "Halo dari mock") {
		t.Fatalf("unexpected content: %q", resp.Content)
	}
	if resp.PromptTokens != 10 || resp.CompletionTokens != 5 {
		t.Fatalf("unexpected usage: %+v", resp)
	}
}

func TestGenerateStream(t *testing.T) {
	chunks := []string{
		`{"id":"c1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Hal"}}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"content":"o"}}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[]}`,
		`data: [DONE]`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)

	var gotContent string
	req := provider.ChatRequest{
		Model:  "test-model",
		Stream: true,
		StreamCallback: func(chunk provider.StreamChunk) {
			gotContent += chunk.Content
		},
		Messages: []provider.ChatMessage{
			{Role: provider.RoleUser, Content: "hello"},
		},
	}
	resp, err := c.Generate(context.Background(), "", req)
	if err != nil {
		t.Fatalf("stream generate: %v", err)
	}
	if gotContent != "Halo" {
		t.Fatalf("unexpected streamed content: %q", gotContent)
	}
	if resp.Content != "Halo" {
		t.Fatalf("unexpected accumulated content: %q", resp.Content)
	}
}

func TestGenerateWithCombo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"x","object":"chat.completion","created":0,"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"combo ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	db := c.db

	combo := &storage.ModelComboRecord{
		ID:       "c1",
		Name:     "smart",
		Targets:  []storage.ComboTarget{{ProviderID: "mock", Model: "test-model"}},
		Strategy: "failsafe",
		IsActive: true,
	}
	if err := db.SaveCombo(combo); err != nil {
		t.Fatalf("save combo: %v", err)
	}

	resp, err := c.Generate(context.Background(), "smart", provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("combo generate: %v", err)
	}
	if !strings.Contains(resp.Content, "combo ok") {
		t.Fatalf("unexpected combo content: %q", resp.Content)
	}
}

func TestToBifrostMessagesImages(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: provider.RoleUser, Content: "look", Images: []string{"data:image/png;base64,AAA"}},
	}
	bmsgs := toBifrostMessages(msgs)
	if len(bmsgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(bmsgs))
	}
	bm := bmsgs[0]
	if bm.Content == nil || len(bm.Content.ContentBlocks) != 2 {
		t.Fatalf("expected 2 content blocks, got %+v", bm.Content)
	}
	if bm.Content.ContentBlocks[1].Type != "image_url" {
		t.Fatalf("expected image block, got %s", bm.Content.ContentBlocks[1].Type)
	}
}

func TestMapProviderType(t *testing.T) {
	cases := map[string]string{
		"9router":     "openai",
		"anthropic":   "anthropic",
		"gemini":      "gemini",
		"ollama":      "ollama",
		"free_openai": "openai",
		"custom":      "openai",
	}
	for in, want := range cases {
		if got := string(mapBaseProviderType(in)); got != want {
			t.Fatalf("mapBaseProviderType(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	tests := map[string]string{
		"https://gateway.example/v1":  "https://gateway.example",
		"https://gateway.example/v1/": "https://gateway.example",
		"https://gateway.example/v2":  "https://gateway.example/v2",
		"https://gateway.example/":    "https://gateway.example",
		"":                            "",
	}
	for input, expected := range tests {
		if got := normalizeBaseURL(input, "custom"); got != expected {
			t.Errorf("normalizeBaseURL(%q) = %q; want %q", input, got, expected)
		}
	}

	geminiTests := map[string]string{
		"https://generativelanguage.googleapis.com":        "https://generativelanguage.googleapis.com/v1beta",
		"https://generativelanguage.googleapis.com/v1beta": "https://generativelanguage.googleapis.com/v1beta",
		"":                                                 "https://generativelanguage.googleapis.com/v1beta",
	}
	for input, expected := range geminiTests {
		if got := normalizeBaseURL(input, "gemini"); got != expected {
			t.Errorf("normalizeBaseURL(%q, gemini) = %q; want %q", input, got, expected)
		}
	}
}
