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

// FirecrawlProvider implements the Provider interface using Firecrawl Search API
type FirecrawlProvider struct {
	cfg    config.FirecrawlConfig
	client *http.Client
}

// NewFirecrawlProvider creates a new Firecrawl search provider
func NewFirecrawlProvider(cfg config.FirecrawlConfig, timeout time.Duration) *FirecrawlProvider {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.firecrawl.dev"
	}
	cfg.BaseURL = baseURL

	return &FirecrawlProvider{
		cfg: cfg,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (f *FirecrawlProvider) Name() string {
	return "firecrawl"
}

func (f *FirecrawlProvider) IsAvailable() bool {
	return strings.TrimSpace(f.cfg.APIKey) != ""
}

type firecrawlScrapeOptions struct {
	Formats []string `json:"formats,omitempty"`
}

type firecrawlSearchRequest struct {
	Query         string                  `json:"query"`
	Limit         int                     `json:"limit,omitempty"`
	ScrapeOptions *firecrawlScrapeOptions `json:"scrapeOptions,omitempty"`
}

type firecrawlSearchItem struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Markdown    string `json:"markdown"`
}

type firecrawlSearchResponse struct {
	Success bool                  `json:"success"`
	Data    []firecrawlSearchItem `json:"data"`
	Error   string                `json:"error,omitempty"`
	Message string                `json:"message,omitempty"`
}

func (f *FirecrawlProvider) Search(ctx context.Context, query string, limit int) (*Response, error) {
	if !f.IsAvailable() {
		return nil, fmt.Errorf("firecrawl API key belum dikonfigurasi")
	}

	if limit <= 0 {
		limit = 5
	}

	reqBody := firecrawlSearchRequest{
		Query: query,
		Limit: limit,
		ScrapeOptions: &firecrawlScrapeOptions{
			Formats: []string{"markdown"},
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("gagal serialize request firecrawl: %w", err)
	}

	endpoint := "/v1/search"
	if strings.HasSuffix(f.cfg.BaseURL, "/v1") {
		endpoint = "/search"
	}
	targetURL := fmt.Sprintf("%s%s", f.cfg.BaseURL, endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gagal membuat request firecrawl: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", f.cfg.APIKey))
	req.Header.Set("User-Agent", "GoAssistant/1.0")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal memanggil firecrawl API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca response firecrawl: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp firecrawlSearchResponse
		_ = json.Unmarshal(bodyBytes, &errResp)
		errMsg := errResp.Error
		if errMsg == "" {
			errMsg = errResp.Message
		}
		if errMsg == "" {
			errMsg = string(bodyBytes)
		}
		return nil, fmt.Errorf("firecrawl API mengembalikan status %d: %s", resp.StatusCode, errMsg)
	}

	var res firecrawlSearchResponse
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return nil, fmt.Errorf("gagal parsing response firecrawl: %w", err)
	}

	if !res.Success && len(res.Data) == 0 && res.Error != "" {
		return nil, fmt.Errorf("firecrawl error: %s", res.Error)
	}

	items := make([]SearchItem, 0, len(res.Data))
	for _, item := range res.Data {
		snippet := strings.TrimSpace(item.Description)
		if snippet == "" && item.Markdown != "" {
			// Extract a concise snippet from markdown (max 400 chars)
			md := strings.ReplaceAll(item.Markdown, "\n", " ")
			md = strings.TrimSpace(md)
			if len(md) > 400 {
				snippet = md[:400] + "..."
			} else {
				snippet = md
			}
		}

		items = append(items, SearchItem{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: snippet,
		})
	}

	return &Response{
		Query:    query,
		Provider: f.Name(),
		Results:  items,
	}, nil
}
