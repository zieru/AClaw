package tools

import (
	"context"
	"strings"
	"testing"

	"goassistant/internal/config"
	"goassistant/internal/search"
)

type mockSearchProvider struct {
	name string
	res  *search.Response
	err  error
}

func (m *mockSearchProvider) Name() string { return m.name }
func (m *mockSearchProvider) IsAvailable() bool { return true }
func (m *mockSearchProvider) Search(ctx context.Context, query string, limit int) (*search.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.res, nil
}

func TestWebSearchTool_Execute(t *testing.T) {
	cfg := config.SearchConfig{
		Enabled:    true,
		Provider:   "tavily",
		MaxResults: 5,
	}
	eng := search.InitGlobalEngine(cfg)

	// Register mock provider on engine
	mock := &mockSearchProvider{
		name: "tavily",
		res: &search.Response{
			Query:    "golang",
			Answer:   "Go is created by Google.",
			Provider: "tavily",
			Results: []search.SearchItem{
				{
					Title:   "The Go Programming Language",
					URL:     "https://go.dev",
					Snippet: "Build fast, reliable, and efficient software at scale.",
				},
			},
		},
	}
	eng.RegisterProvider(mock)

	tool := NewWebSearchTool(eng)

	if tool.Name() != "web_search" {
		t.Errorf("expected name web_search, got %s", tool.Name())
	}

	// 1. Missing query
	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error on missing query")
	}

	// 2. Successful search
	out, err := tool.Execute(context.Background(), map[string]interface{}{"query": "golang"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Hasil Pencarian Web untuk: golang (via tavily)") {
		t.Errorf("expected header in output, got: %s", out)
	}
	if !strings.Contains(out, "Go is created by Google.") {
		t.Errorf("expected answer in output, got: %s", out)
	}
	if !strings.Contains(out, "https://go.dev") {
		t.Errorf("expected URL in output, got: %s", out)
	}
}
