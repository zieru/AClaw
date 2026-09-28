package omniroute

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
)

// HealthResponse represents OmniRoute's /api/health response
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// AnalyticsSummary represents the aggregated summary inside /api/usage/analytics
type AnalyticsSummary struct {
	TotalRequests      int     `json:"totalRequests"`
	PromptTokens       int     `json:"promptTokens"`
	CompletionTokens   int     `json:"completionTokens"`
	TotalTokens        int     `json:"totalTokens"`
	UniqueModels       int     `json:"uniqueModels"`
	UniqueAccounts     int     `json:"uniqueAccounts"`
	SuccessfulRequests int     `json:"successfulRequests"`
	SuccessRatePct     float64 `json:"successRatePct"`
	AvgLatencyMs       int     `json:"avgLatencyMs"`
	TotalCost          float64 `json:"totalCost"`
}

// AnalyticsResponse represents /api/usage/analytics response
type AnalyticsResponse struct {
	Summary AnalyticsSummary `json:"summary"`
}

// SearchResultItem represents an item in POST /v1/search response
type SearchResultItem struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	DisplayURL string `json:"display_url,omitempty"`
	Snippet    string `json:"snippet"`
}

// SearchResponse represents POST /v1/search response
type SearchResponse struct {
	ID       string             `json:"id"`
	Provider string             `json:"provider"`
	Query    string             `json:"query"`
	Results  []SearchResultItem `json:"results"`
}

// MemoryItem represents a memory item from /api/memory
type MemoryItem struct {
	ID             string                 `json:"id"`
	APIKeyID       string                 `json:"apiKeyId,omitempty"`
	SessionID      string                 `json:"sessionId,omitempty"`
	Type           string                 `json:"type"` // e.g. "factual"
	Key            string                 `json:"key"`
	Content        string                 `json:"content"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt      string                 `json:"createdAt,omitempty"`
	UpdatedAt      string                 `json:"updatedAt,omitempty"`
	AccessCount    int                    `json:"accessCount,omitempty"`
	LastAccessedAt string                 `json:"lastAccessedAt,omitempty"`
}

// MemoryListResponse represents GET /api/memory response
type MemoryListResponse struct {
	Data []MemoryItem `json:"data"`
}

// Client interacts directly with the co-located OmniRoute gateway
type Client struct {
	baseURL    string
	password   string
	apiKey     string
	authToken  string
	httpClient *http.Client
	mu         sync.RWMutex
}

var (
	defaultClient *Client
	clientMu      sync.RWMutex
)

// InitClient initializes the singleton OmniRoute client from config
func InitClient(cfg config.OmniRouteConfig) *Client {
	clientMu.Lock()
	defer clientMu.Unlock()

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:20128"
	}

	defaultClient = &Client{
		baseURL:  baseURL,
		password: cfg.Password,
		apiKey:   cfg.APIKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
	return defaultClient
}

// GetClient returns the initialized global OmniRoute client
func GetClient() *Client {
	clientMu.RLock()
	defer clientMu.RUnlock()
	return defaultClient
}

// SetClient sets a custom client (useful for unit tests)
func SetClient(c *Client) {
	clientMu.Lock()
	defer clientMu.Unlock()
	defaultClient = c
}

// BaseURL returns the configured base URL
func (c *Client) BaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baseURL
}

// Login authenticates to OmniRoute (/api/auth/login) using the master password
func (c *Client) Login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.password == "" && c.apiKey != "" {
		return nil // Using direct API key instead
	}

	loginURL := fmt.Sprintf("%s/api/auth/login", c.baseURL)
	payload := map[string]string{"password": c.password}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal login payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	// Extract auth_token cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "auth_token" {
			c.authToken = cookie.Value
			return nil
		}
	}

	return nil
}

// doRequest performs an HTTP request with auth headers and handles token refresh
func (c *Client) doRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	url := fmt.Sprintf("%s%s", c.baseURL, path)

	c.mu.RLock()
	token := c.authToken
	apiKey := c.apiKey
	c.mu.RUnlock()

	makeReq := func(curToken string) (*http.Request, error) {
		var bodyReader io.Reader
		if body != nil {
			bodyReader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if apiKey != "" {
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
		}
		if curToken != "" {
			req.AddCookie(&http.Cookie{Name: "auth_token", Value: curToken})
		}
		return req, nil
	}

	// If no token and password exists, login first
	if token == "" && apiKey == "" && c.password != "" {
		if err := c.Login(ctx); err != nil {
			// Don't fail immediately, proceed anyway in case endpoint is public
		}
		c.mu.RLock()
		token = c.authToken
		c.mu.RUnlock()
	}

	req, err := makeReq(token)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	// If 401 Unauthorized and password is set, try re-authenticating once
	if resp.StatusCode == http.StatusUnauthorized && c.password != "" {
		resp.Body.Close()
		if err := c.Login(ctx); err != nil {
			return nil, fmt.Errorf("re-login failed: %w", err)
		}
		c.mu.RLock()
		token = c.authToken
		c.mu.RUnlock()

		reqRetry, err := makeReq(token)
		if err != nil {
			return nil, err
		}
		return c.httpClient.Do(reqRetry)
	}

	return resp, nil
}

// GetHealth queries /api/health on OmniRoute
func (c *Client) GetHealth(ctx context.Context) (*HealthResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/health", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("health check failed with status %d: %s", resp.StatusCode, string(body))
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("decode health response: %w", err)
	}
	return &health, nil
}

// GetAnalytics queries /api/usage/analytics on OmniRoute
func (c *Client) GetAnalytics(ctx context.Context) (*AnalyticsResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/usage/analytics", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get analytics failed with status %d: %s", resp.StatusCode, string(body))
	}

	var analytics AnalyticsResponse
	if err := json.NewDecoder(resp.Body).Decode(&analytics); err != nil {
		return nil, fmt.Errorf("decode analytics response: %w", err)
	}
	return &analytics, nil
}

// GetRequestLogs queries /api/usage/request-logs on OmniRoute
func (c *Client) GetRequestLogs(ctx context.Context) ([]string, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/usage/request-logs", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get request logs failed with status %d: %s", resp.StatusCode, string(body))
	}

	var logs []string
	if err := json.NewDecoder(resp.Body).Decode(&logs); err != nil {
		return nil, fmt.Errorf("decode request logs: %w", err)
	}
	return logs, nil
}

// Search queries POST /v1/search on OmniRoute
func (c *Client) Search(ctx context.Context, query string) (*SearchResponse, error) {
	payload := map[string]string{"query": query}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal search payload: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/v1/search", data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	return &result, nil
}

// ListMemories queries GET /api/memory on OmniRoute
func (c *Client) ListMemories(ctx context.Context) ([]MemoryItem, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/memory", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list memories failed with status %d: %s", resp.StatusCode, string(body))
	}

	var memResp MemoryListResponse
	if err := json.NewDecoder(resp.Body).Decode(&memResp); err != nil {
		return nil, fmt.Errorf("decode memory response: %w", err)
	}
	return memResp.Data, nil
}

// SaveMemory creates or updates a memory item on OmniRoute via POST /api/memory
func (c *Client) SaveMemory(ctx context.Context, key, content, memType string, metadata map[string]interface{}) error {
	if memType == "" {
		memType = "factual"
	}
	payload := map[string]interface{}{
		"type":     memType,
		"key":      key,
		"content":  content,
		"metadata": metadata,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal memory payload: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/api/memory", data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("save memory failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
