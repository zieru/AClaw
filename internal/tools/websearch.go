package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type WebSearchTool struct{}

func (t *WebSearchTool) Name() string {
	return "web_search"
}

func (t *WebSearchTool) Description() string {
	return "Mencari informasi terkini dari internet menggunakan web search query."
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

type ddgResponse struct {
	AbstractText string `json:"AbstractText"`
	AbstractURL  string `json:"AbstractURL"`
	Heading      string `json:"Heading"`
	RelatedTopics []struct {
		Text     string `json:"Text"`
		FirstURL string `json:"FirstURL"`
	} `json:"RelatedTopics"`
}

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	q, ok := args["query"].(string)
	if !ok || strings.TrimSpace(q) == "" {
		return "", fmt.Errorf("parameter 'query' wajib diisi")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	reqURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1&skip_disambig=1", url.QueryEscape(q))
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "GoAssistant/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gagal melakukan pencarian web: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var data ddgResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return string(body[:min(len(body), 500)]), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Hasil Pencarian Web untuk: %s\n\n", q))
	if data.AbstractText != "" {
		sb.WriteString(fmt.Sprintf("- Ringkasan: %s\n  Sumber: %s\n\n", data.AbstractText, data.AbstractURL))
	}

	count := 0
	for _, topic := range data.RelatedTopics {
		if topic.Text != "" && count < 5 {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", topic.Text, topic.FirstURL))
			count++
		}
	}

	if sb.Len() == len(fmt.Sprintf("Hasil Pencarian Web untuk: %s\n\n", q)) {
		sb.WriteString("Tidak ada ringkasan instan langsung ditemukan. Silakan gunakan query yang lebih spesifik.")
	}

	return sb.String(), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
