package search

import (
	"testing"
)

func TestGeminiWebProvider_NameAndAvailability(t *testing.T) {
	p := NewGeminiWebSearchProvider()
	if p.Name() != "gemini_web" {
		t.Errorf("expected provider name 'gemini_web', got '%s'", p.Name())
	}
}

func TestParseGeminiWebSearchResponse_JSONBlock(t *testing.T) {
	rawContent := `Berikut ringkasan berita Go:
Go 1.24 menghadirkan generic type alias dan peningkatan performa signifikan.

### Top Search Results
` + "```json" + `
[
  {
    "title": "Go 1.24 Release Notes",
    "url": "https://go.dev/doc/go1.24",
    "snippet": "Go 1.24 introduces generic type aliases and runtime improvements."
  },
  {
    "title": "Better Stack Go 1.24 Guide",
    "url": "https://betterstack.com/community/guides/scaling-go/go-1-24/",
    "snippet": "Detailed guide on what is new in Go 1.24."
  }
]
` + "```"

	resp, err := parseGeminiWebSearchResponse("Go 1.24", rawContent, 5)
	if err != nil {
		t.Fatalf("unexpected error parsing search response: %v", err)
	}

	if resp.Provider != "gemini_web" {
		t.Errorf("expected provider 'gemini_web', got '%s'", resp.Provider)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if resp.Results[0].Title != "Go 1.24 Release Notes" {
		t.Errorf("unexpected first title: %s", resp.Results[0].Title)
	}
	if resp.Results[0].URL != "https://go.dev/doc/go1.24" {
		t.Errorf("unexpected first url: %s", resp.Results[0].URL)
	}
	if resp.Answer == "" {
		t.Errorf("expected answer summary to be extracted")
	}
}

func TestParseGeminiWebSearchResponse_MarkdownFallback(t *testing.T) {
	rawContent := `Berikut adalah hasil pencarian:
* [Go Official Site](https://go.dev) - Situs resmi bahasa pemrograman Go.
* [Golang GitHub](https://github.com/golang/go) - Repositori kode sumber Go.`

	resp, err := parseGeminiWebSearchResponse("Golang official", rawContent, 5)
	if err != nil {
		t.Fatalf("unexpected error parsing markdown fallback: %v", err)
	}

	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 parsed markdown links, got %d", len(resp.Results))
	}
	if resp.Results[0].URL != "https://go.dev" {
		t.Errorf("expected https://go.dev, got %s", resp.Results[0].URL)
	}
	if resp.Results[1].URL != "https://github.com/golang/go" {
		t.Errorf("expected https://github.com/golang/go, got %s", resp.Results[1].URL)
	}
}
