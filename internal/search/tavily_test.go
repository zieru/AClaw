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

func TestTavilyProvider_Search(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/search" {
			t.Errorf("expected /search, got %s", r.URL.Path)
		}

		var req tavilySearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.APIKey != "tvly-test-123" {
			t.Errorf("expected api_key tvly-test-123, got %s", req.APIKey)
		}
		if req.Query != "golang news" {
			t.Errorf("expected query 'golang news', got %s", req.Query)
		}

		resp := tavilySearchResponse{
			Query:  req.Query,
			Answer: "Golang 1.24 has been released.",
			Results: []struct {
				Title   string  `json:"title"`
				URL     string  `json:"url"`
				Content string  `json:"content"`
				Score   float64 `json:"score"`
			}{
				{
					Title:   "Go 1.24 Release Notes",
					URL:     "https://go.dev/doc/go1.24",
					Content: "Go 1.24 includes many new features and improvements.",
					Score:   0.98,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cfg := config.TavilyConfig{
		APIKey:        "tvly-test-123",
		BaseURL:       ts.URL,
		SearchDepth:   "basic",
		IncludeAnswer: true,
	}

	p := NewTavilyProvider(cfg, 5*time.Second)
	if !p.IsAvailable() {
		t.Fatal("expected provider to be available")
	}

	res, err := p.Search(context.Background(), "golang news", 5)
	if err != nil {
		t.Fatalf("unexpected search error: %v", err)
	}

	if res.Provider != "tavily" {
		t.Errorf("expected provider tavily, got %s", res.Provider)
	}
	if res.Answer != "Golang 1.24 has been released." {
		t.Errorf("unexpected answer: %s", res.Answer)
	}
	if len(res.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res.Results))
	}
	if res.Results[0].Title != "Go 1.24 Release Notes" {
		t.Errorf("unexpected result title: %s", res.Results[0].Title)
	}
}

func TestTavilyProvider_Error(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"Invalid API key"}`))
	}))
	defer ts.Close()

	cfg := config.TavilyConfig{
		APIKey:  "tvly-invalid",
		BaseURL: ts.URL,
	}

	p := NewTavilyProvider(cfg, 5*time.Second)
	_, err := p.Search(context.Background(), "test", 5)
	if err == nil {
		t.Fatal("expected error on 401 Unauthorized")
	}
}
