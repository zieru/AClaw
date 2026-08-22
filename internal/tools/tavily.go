package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"goassistant/internal/config"
)

// TavilySearchTool searches the web using the Tavily AI search engine
type TavilySearchTool struct{}

func (t *TavilySearchTool) Name() string {
	return "tavily_search"
}

func (t *TavilySearchTool) Description() string {
	return "Mencari informasi terkini dan paling relevan dari internet menggunakan Tavily AI Search API."
}

func (t *TavilySearchTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"query": {
				Type:        "string",
				Description: "Kata kunci atau pertanyaan pencarian di internet.",
			},
			"search_depth": {
				Type:        "string",
				Description: "Kedalaman pencarian: 'basic' (cepat) atau 'advanced' (mendalam). Default: basic.",
				Enum:        []string{"basic", "advanced"},
			},
		},
		Required: []string{"query"},
	}
}

type tavilyRequest struct {
	APIKey        string   `json:"api_key"`
	Query         string   `json:"query"`
	SearchDepth   string   `json:"search_depth,omitempty"`
	IncludeAnswer bool     `json:"include_answer"`
	MaxResults    int      `json:"max_results,omitempty"`
	IncludeImages bool     `json:"include_images"`
}

type tavilyResultItem struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

type tavilyResponse struct {
	Query   string             `json:"query"`
	Answer  string             `json:"answer"`
	Results []tavilyResultItem `json:"results"`
}

func (t *TavilySearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	q, ok := args["query"].(string)
	if !ok || strings.TrimSpace(q) == "" {
		return "", fmt.Errorf("parameter 'query' wajib diisi")
	}

	// Check Tool Cache
	if cached, hit := GetGlobalToolCache().Get(t.Name(), args); hit {
		return cached, nil
	}

	apiKey := ""
	searchDepth := "basic"
	maxResults := 5

	if cfg := config.Get(); cfg != nil {
		apiKey = cfg.Tavily.APIKey
		if cfg.Tavily.SearchDepth != "" {
			searchDepth = cfg.Tavily.SearchDepth
		}
		if cfg.Tavily.MaxResults > 0 {
			maxResults = cfg.Tavily.MaxResults
		}
	}

	if envKey := os.Getenv("TAVILY_API_KEY"); envKey != "" {
		apiKey = envKey
	}

	if apiKey == "" {
		return "", fmt.Errorf("Tavily API Key belum dikonfigurasi. Silakan atur di configs/default_config.yaml atau set env TAVILY_API_KEY")
	}

	if depthParam, ok := args["search_depth"].(string); ok && depthParam != "" {
		searchDepth = depthParam
	}

	reqPayload := tavilyRequest{
		APIKey:        apiKey,
		Query:         q,
		SearchDepth:   searchDepth,
		IncludeAnswer: true,
		MaxResults:    maxResults,
		IncludeImages: false,
	}

	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("gagal serialize request Tavily: %w", err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GoAssistant/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gagal melakukan pencarian Tavily: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Tavily API error (%d): %s", resp.StatusCode, string(body))
	}

	var data tavilyResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return string(body), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 <b>Hasil Pencarian Tavily untuk:</b> %s\n\n", q))

	if data.Answer != "" {
		sb.WriteString(fmt.Sprintf("💡 <b>Ringkasan AI:</b>\n%s\n\n", data.Answer))
	}

	if len(data.Results) > 0 {
		sb.WriteString("🌐 <b>Sumber Terkait:</b>\n")
		for i, res := range data.Results {
			sb.WriteString(fmt.Sprintf("%d. <b>%s</b>\n   🔗 <a href=\"%s\">%s</a>\n", i+1, res.Title, res.URL, res.URL))
			if res.Content != "" {
				snippet := res.Content
				if len(snippet) > 300 {
					snippet = snippet[:300] + "..."
				}
				sb.WriteString(fmt.Sprintf("   📄 %s\n\n", snippet))
			}
		}
	} else if data.Answer == "" {
		sb.WriteString("Tidak ada hasil yang ditemukan.")
	}

	resText := sb.String()
	GetGlobalToolCache().Set(t.Name(), args, resText, 30*time.Minute)
	return resText, nil
}
