package search

import "context"

// SearchItem represents a single web search hit
type SearchItem struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score,omitempty"`
}

// Response represents a normalized response from any search provider
type Response struct {
	Query    string       `json:"query"`
	Answer   string       `json:"answer,omitempty"` // Instant answer if provided (e.g. Tavily AI answer)
	Provider string       `json:"provider"`         // Name of the provider that fulfilled the request
	Results  []SearchItem `json:"results"`
}

// Provider defines the interface for an external web search provider
type Provider interface {
	Name() string
	Search(ctx context.Context, query string, limit int) (*Response, error)
	IsAvailable() bool
}
