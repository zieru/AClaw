package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DuckDuckGoProvider provides a zero-auth fallback search provider
type DuckDuckGoProvider struct {
	baseURL string
	client  *http.Client
}

// NewDuckDuckGoProvider creates a new DuckDuckGo search provider
func NewDuckDuckGoProvider(timeout time.Duration) *DuckDuckGoProvider {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &DuckDuckGoProvider{
		baseURL: "https://api.duckduckgo.com",
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (d *DuckDuckGoProvider) Name() string {
	return "duckduckgo"
}

func (d *DuckDuckGoProvider) IsAvailable() bool {
	return true
}

type ddgResponse struct {
	AbstractText  string `json:"AbstractText"`
	AbstractURL   string `json:"AbstractURL"`
	Heading       string `json:"Heading"`
	RelatedTopics []struct {
		Text     string `json:"Text"`
		FirstURL string `json:"FirstURL"`
	} `json:"RelatedTopics"`
}

func (d *DuckDuckGoProvider) Search(ctx context.Context, query string, limit int) (*Response, error) {
	if limit <= 0 {
		limit = 5
	}

	baseURL := d.baseURL
	if baseURL == "" {
		baseURL = "https://api.duckduckgo.com"
	}

	reqURL := fmt.Sprintf("%s/?q=%s&format=json&no_html=1&skip_disambig=1", baseURL, url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gagal membuat request duckduckgo: %w", err)
	}
	req.Header.Set("User-Agent", "GoAssistant/1.0")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal memanggil duckduckgo: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca response duckduckgo: %w", err)
	}

	var data ddgResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("gagal unmarshal response duckduckgo: %w", err)
	}

	var items []SearchItem
	if data.AbstractText != "" {
		heading := data.Heading
		if heading == "" {
			heading = query
		}
		items = append(items, SearchItem{
			Title:   heading,
			URL:     data.AbstractURL,
			Snippet: data.AbstractText,
		})
	}

	for _, topic := range data.RelatedTopics {
		if len(items) >= limit {
			break
		}
		if topic.Text != "" {
			items = append(items, SearchItem{
				Title:   topic.Text,
				URL:     topic.FirstURL,
				Snippet: topic.Text,
			})
		}
	}

	return &Response{
		Query:    query,
		Provider: d.Name(),
		Results:  items,
	}, nil
}
