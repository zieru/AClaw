package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDuckDuckGoProvider_Search(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"AbstractText": "Golang is an open-source programming language supported by Google.",
			"AbstractURL": "https://en.wikipedia.org/wiki/Go_(programming_language)",
			"Heading": "Go (programming language)",
			"RelatedTopics": [
				{
					"Text": "Go standard library documentation",
					"FirstURL": "https://pkg.go.dev/std"
				}
			]
		}`))
	}))
	defer ts.Close()

	p := &DuckDuckGoProvider{
		baseURL: ts.URL,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}

	if !p.IsAvailable() {
		t.Fatal("duckduckgo should always be available")
	}

	res, err := p.Search(context.Background(), "golang", 5)
	if err != nil {
		t.Fatalf("unexpected search error: %v", err)
	}

	if res.Provider != "duckduckgo" {
		t.Errorf("expected provider duckduckgo, got %s", res.Provider)
	}

	if len(res.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res.Results))
	}

	if res.Results[0].Title != "Go (programming language)" {
		t.Errorf("unexpected first title: %s", res.Results[0].Title)
	}
	if res.Results[1].Title != "Go standard library documentation" {
		t.Errorf("unexpected second title: %s", res.Results[1].Title)
	}
}
