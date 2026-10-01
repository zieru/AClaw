package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goassistant/internal/search"
)

// WebSearchTool provides capability for LLMs to query live web information
type WebSearchTool struct {
	engine *search.Engine
}

// NewWebSearchTool creates a new WebSearchTool instance with an optional custom engine
func NewWebSearchTool(engine *search.Engine) *WebSearchTool {
	return &WebSearchTool{
		engine: engine,
	}
}

func (t *WebSearchTool) Name() string {
	return "web_search"
}

func (t *WebSearchTool) Description() string {
	return "Mencari informasi terkini dari internet menggunakan multi-provider search engine (Tavily, Firecrawl, DuckDuckGo)."
}

func (t *WebSearchTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"query": {
				Type:        "string",
				Description: "Kata kunci pencarian yang ingin dicari di internet.",
			},
		},
		Required: []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	q, ok := args["query"].(string)
	if !ok || strings.TrimSpace(q) == "" {
		return "", fmt.Errorf("parameter 'query' wajib diisi")
	}
	q = strings.TrimSpace(q)

	// Check global cache
	if cachedVal, found := GetGlobalToolCache().Get(t.Name(), args); found {
		return cachedVal, nil
	}

	// Use injected engine or singleton
	eng := t.engine
	if eng == nil {
		eng = search.GetGlobalEngine()
	}

	searchRes, err := eng.Search(ctx, q)
	if err != nil {
		return "", fmt.Errorf("gagal melakukan pencarian web: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Hasil Pencarian Web untuk: %s (via %s)\n\n", q, searchRes.Provider))

	if searchRes.Answer != "" {
		sb.WriteString(fmt.Sprintf("💡 Ringkasan Jawaban AI:\n%s\n\n", searchRes.Answer))
	}

	if len(searchRes.Results) == 0 {
		sb.WriteString("Tidak ada hasil dokumen spesifik yang ditemukan.")
	} else {
		for i, res := range searchRes.Results {
			snippet := strings.TrimSpace(res.Snippet)
			if snippet != "" {
				sb.WriteString(fmt.Sprintf("%d. %s\n   URL: %s\n   Ringkasan: %s\n\n", i+1, res.Title, res.URL, snippet))
			} else {
				sb.WriteString(fmt.Sprintf("%d. %s\n   URL: %s\n\n", i+1, res.Title, res.URL))
			}
		}
	}

	out := sb.String()
	GetGlobalToolCache().Set(t.Name(), args, out, 30*time.Minute)
	return out, nil
}
