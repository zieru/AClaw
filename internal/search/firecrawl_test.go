package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"goassistant/internal/config"
)

func TestFirecrawlProvider_Search(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/search" {
			t.Errorf("expected /v1/search, got %s", r.URL.Path)
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer fc-test-key" {
			t.Errorf("expected Bearer fc-test-key, got %s", authHeader)
		}

		var req firecrawlSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.Query != "firecrawl scraping" {
			t.Errorf("expected query 'firecrawl scraping', got %s", req.Query)
		}

		resp := firecrawlSearchResponse{
			Success: true,
			Data: []firecrawlSearchItem{
				{
					URL:         "https://firecrawl.dev",
					Title:       "Firecrawl - Turn websites into LLM-ready data",
					Description: "Crawl and convert any website into clean markdown.",
					Markdown:    "# Firecrawl Documentation\nClean markdown content.",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cfg := config.FirecrawlConfig{
		APIKey:  "fc-test-key",
		BaseURL: ts.URL,
	}

	p := NewFirecrawlProvider(cfg, 5*time.Second)
	if !p.IsAvailable() {
		t.Fatal("expected provider to be available")
	}

	res, err := p.Search(context.Background(), "firecrawl scraping", 5)
	if err != nil {
		t.Fatalf("unexpected search error: %v", err)
	}

	if res.Provider != "firecrawl" {
		t.Errorf("expected provider firecrawl, got %s", res.Provider)
	}
	if len(res.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res.Results))
	}
	if res.Results[0].Title != "Firecrawl - Turn websites into LLM-ready data" {
		t.Errorf("unexpected result title: %s", res.Results[0].Title)
	}
	if res.Results[0].Snippet != "Crawl and convert any website into clean markdown." {
		t.Errorf("unexpected snippet: %s", res.Results[0].Snippet)
	}
}

func TestFirecrawlProvider_Error(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"success":false,"error":"Payment required / rate limit exceeded"}`))
	}))
	defer ts.Close()

	cfg := config.FirecrawlConfig{
		APIKey:  "fc-test-key",
		BaseURL: ts.URL,
	}

	p := NewFirecrawlProvider(cfg, 5*time.Second)
	_, err := p.Search(context.Background(), "test", 5)
	if err == nil {
		t.Fatal("expected error on 402 Payment Required")
	}
}
