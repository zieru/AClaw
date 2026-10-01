package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"goassistant/internal/config"
)

// TavilyProvider implements the Provider interface using Tavily Search API
type TavilyProvider struct {
	cfg    config.TavilyConfig
	client *http.Client
}

// NewTavilyProvider creates a new Tavily search provider
func NewTavilyProvider(cfg config.TavilyConfig, timeout time.Duration) *TavilyProvider {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.tavily.com"
	}
	cfg.BaseURL = baseURL

	return &TavilyProvider{
		cfg: cfg,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (t *TavilyProvider) Name() string {
	return "tavily"
}

func (t *TavilyProvider) IsAvailable() bool {
	return strings.TrimSpace(t.cfg.APIKey) != ""
}

type tavilySearchRequest struct {
	APIKey        string `json:"api_key"`
	Query         string `json:"query"`
	SearchDepth   string `json:"search_depth,omitempty"`
	IncludeAnswer bool   `json:"include_answer"`
	MaxResults    int    `json:"max_results,omitempty"`
}

type tavilySearchResponse struct {
	Query   string `json:"query"`
	Answer  string `json:"answer"`
	Results []struct {
		Title   string  `json:"title"`
		URL     string  `json:"url"`
		Content string  `json:"content"`
		Score   float64 `json:"score"`
	} `json:"results"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (t *TavilyProvider) Search(ctx context.Context, query string, limit int) (*Response, error) {
	if !t.IsAvailable() {
		return nil, fmt.Errorf("tavily API key belum dikonfigurasi")
	}

	searchDepth := t.cfg.SearchDepth
	if searchDepth == "" {
		searchDepth = "basic"
	}
	if limit <= 0 {
		limit = 5
	}

	reqBody := tavilySearchRequest{
		APIKey:        t.cfg.APIKey,
		Query:         query,
		SearchDepth:   searchDepth,
		IncludeAnswer: t.cfg.IncludeAnswer,
		MaxResults:    limit,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("gagal serialize request tavily: %w", err)
	}

	targetURL := fmt.Sprintf("%s/search", t.cfg.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gagal membuat request tavily: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GoAssistant/1.0")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal memanggil tavily API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca response tavily: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp tavilySearchResponse
		_ = json.Unmarshal(bodyBytes, &errResp)
		errMsg := errResp.Detail
		if errMsg == "" {
			errMsg = errResp.Error
		}
		if errMsg == "" {
			errMsg = string(bodyBytes)
		}
		return nil, fmt.Errorf("tavily API mengembalikan status %d: %s", resp.StatusCode, errMsg)
	}

	var res tavilySearchResponse
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return nil, fmt.Errorf("gagal parsing response tavily: %w", err)
	}

	items := make([]SearchItem, 0, len(res.Results))
	for _, r := range res.Results {
		snippet := strings.TrimSpace(r.Content)
		items = append(items, SearchItem{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: snippet,
			Score:   r.Score,
		})
	}

	return &Response{
		Query:    query,
		Answer:   strings.TrimSpace(res.Answer),
		Provider: t.Name(),
		Results:  items,
	}, nil
}
