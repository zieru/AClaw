package search

import (
	"context"
	"errors"
	"testing"

	"goassistant/internal/config"
)

type mockProvider struct {
	name      string
	available bool
	err       error
	res       *Response
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) IsAvailable() bool {
	return m.available
}

func (m *mockProvider) Search(ctx context.Context, query string, limit int) (*Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.res, nil
}

func TestEngine_AutoPrioritizationAndFallback(t *testing.T) {
	cfg := config.SearchConfig{
		Enabled:         true,
		Provider:        "auto",
		MaxResults:      5,
		FallbackEnabled: true,
	}

	eng := &Engine{
		cfg:       cfg,
		providers: make(map[string]Provider),
	}

	// 1. Tavily fails with rate limit
	tavilyMock := &mockProvider{
		name:      "tavily",
		available: true,
		err:       errors.New("rate limit exceeded"),
	}
	// 2. Firecrawl succeeds
	firecrawlMock := &mockProvider{
		name:      "firecrawl",
		available: true,
		res: &Response{
			Query:    "golang",
			Provider: "firecrawl",
			Results: []SearchItem{
				{Title: "Golang website", URL: "https://go.dev", Snippet: "Build simple, secure, scalable systems"},
			},
		},
	}
	// 3. DDG as backup
	ddgMock := &mockProvider{
		name:      "duckduckgo",
		available: true,
		res: &Response{
			Query:    "golang",
			Provider: "duckduckgo",
			Results:  []SearchItem{},
		},
	}

	eng.RegisterProvider(tavilyMock)
	eng.RegisterProvider(firecrawlMock)
	eng.RegisterProvider(ddgMock)

	res, err := eng.Search(context.Background(), "golang")
	if err != nil {
		t.Fatalf("expected search to succeed via firecrawl fallback, got err: %v", err)
	}

	if res.Provider != "firecrawl" {
		t.Errorf("expected provider firecrawl, got %s", res.Provider)
	}
	if len(res.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res.Results))
	}
}

func TestEngine_NoFallbackWhenDisabled(t *testing.T) {
	cfg := config.SearchConfig{
		Enabled:         true,
		Provider:        "tavily",
		MaxResults:      5,
		FallbackEnabled: false,
	}

	eng := &Engine{
		cfg:       cfg,
		providers: make(map[string]Provider),
	}

	tavilyMock := &mockProvider{
		name:      "tavily",
		available: true,
		err:       errors.New("tavily quota exhausted"),
	}
	firecrawlMock := &mockProvider{
		name:      "firecrawl",
		available: true,
		res: &Response{
			Query:    "golang",
			Provider: "firecrawl",
			Results:  []SearchItem{{Title: "Golang", URL: "https://go.dev"}},
		},
	}

	eng.RegisterProvider(tavilyMock)
	eng.RegisterProvider(firecrawlMock)

	_, err := eng.Search(context.Background(), "golang")
	if err == nil {
		t.Fatal("expected search to fail because fallback is disabled")
	}
}

func TestEngine_DisabledConfig(t *testing.T) {
	cfg := config.SearchConfig{
		Enabled: false,
	}

	eng := &Engine{
		cfg:       cfg,
		providers: make(map[string]Provider),
	}

	_, err := eng.Search(context.Background(), "golang")
	if err == nil {
		t.Fatal("expected error when search is disabled")
	}
}

func TestEngine_RoundRobin(t *testing.T) {
	cfg := config.SearchConfig{
		Enabled:         true,
		Provider:        "auto",
		Strategy:        "roundrobin",
		MaxResults:      5,
		FallbackEnabled: true,
	}

	eng := &Engine{
		cfg:       cfg,
		providers: make(map[string]Provider),
	}

	tavilyMock := &mockProvider{
		name:      "tavily",
		available: true,
		res: &Response{
			Query:    "golang",
			Provider: "tavily",
			Results:  []SearchItem{{Title: "Tavily Hit", URL: "https://go.dev"}},
		},
	}
	firecrawlMock := &mockProvider{
		name:      "firecrawl",
		available: true,
		res: &Response{
			Query:    "golang",
			Provider: "firecrawl",
			Results:  []SearchItem{{Title: "Firecrawl Hit", URL: "https://go.dev"}},
		},
	}

	eng.RegisterProvider(tavilyMock)
	eng.RegisterProvider(firecrawlMock)

	// Call 1
	res1, err := eng.Search(context.Background(), "golang")
	if err != nil {
		t.Fatalf("call 1 failed: %v", err)
	}

	// Call 2
	res2, err := eng.Search(context.Background(), "golang")
	if err != nil {
		t.Fatalf("call 2 failed: %v", err)
	}

	// Providers between call 1 and call 2 should alternate (one is tavily, the other is firecrawl)
	if res1.Provider == res2.Provider {
		t.Errorf("expected different providers in round-robin, got %s and %s", res1.Provider, res2.Provider)
	}
}

func TestEngine_DynamicSetters(t *testing.T) {
	eng := InitGlobalEngine(config.SearchConfig{
		Enabled:  true,
		Provider: "auto",
		Strategy: "fallback",
	})

	eng.SetStrategy("roundrobin")
	if eng.Strategy() != "roundrobin" {
		t.Errorf("expected roundrobin, got %s", eng.Strategy())
	}

	eng.SetStrategy("fallback")
	if eng.Strategy() != "fallback" {
		t.Errorf("expected fallback, got %s", eng.Strategy())
	}

	eng.SetProvider("firecrawl")
	if eng.ActiveProvider() != "firecrawl" {
		t.Errorf("expected firecrawl, got %s", eng.ActiveProvider())
	}

	eng.SetFallback(false)
	if eng.Config().FallbackEnabled {
		t.Error("expected fallback disabled")
	}

	eng.SetTavilyKey("tvly-dynamic-key")
	if eng.Config().Tavily.APIKey != "tvly-dynamic-key" {
		t.Errorf("unexpected tavily key: %s", eng.Config().Tavily.APIKey)
	}

	eng.SetFirecrawlKey("fc-dynamic-key")
	if eng.Config().Firecrawl.APIKey != "fc-dynamic-key" {
		t.Errorf("unexpected firecrawl key: %s", eng.Config().Firecrawl.APIKey)
	}
}

