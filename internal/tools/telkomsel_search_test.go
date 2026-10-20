package tools

import (
	"context"
	"strings"
	"testing"

	"goassistant/internal/config"
	"goassistant/internal/search"
)

type mockTelkomselSearchProvider struct {
	name string
	res  *search.Response
	err  error
}

func (m *mockTelkomselSearchProvider) Name() string { return m.name }
func (m *mockTelkomselSearchProvider) IsAvailable() bool { return true }
func (m *mockTelkomselSearchProvider) Search(ctx context.Context, query string, limit int) (*search.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.res, nil
}

func TestTelkomselSearchTool_ScopingAndExecution(t *testing.T) {
	eng := search.InitGlobalEngine(config.SearchConfig{
		Enabled:         true,
		Provider:        "tavily",
		FallbackEnabled: false,
		MaxResults:      5,
	})

	mock := &mockTelkomselSearchProvider{
		name: "tavily",
		res: &search.Response{
			Query:    "site:telkomsel.com cara aktivasi esim",
			Answer:   "Aktivasi eSIM dapat dilakukan lewat web atau GraPARI.",
			Provider: "tavily",
			Results: []search.SearchItem{
				{
					Title:   "Panduan eSIM Telkomsel",
					URL:     "https://www.telkomsel.com/esim",
					Snippet: "Beli dan aktifkan eSIM prabayar langsung tanpa kartu fisik.",
				},
			},
		},
	}
	eng.RegisterProvider(mock)

	tool := NewTelkomselSearchTool(eng)
	if tool.Name() != "search_telkomsel_web" {
		t.Errorf("expected tool name 'search_telkomsel_web', got %s", tool.Name())
	}

	// 1. Parameter validation
	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Errorf("expected error when query is empty")
	}

	// 2. Execution with valid query
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query": "cara aktivasi esim",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Panduan eSIM Telkomsel") {
		t.Errorf("expected output to contain title, got: %s", out)
	}
	if !strings.Contains(out, "https://www.telkomsel.com/esim") {
		t.Errorf("expected output to contain url, got: %s", out)
	}
}
