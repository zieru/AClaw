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
