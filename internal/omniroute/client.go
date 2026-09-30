package omniroute

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// MemoryFilter defines optional parameters for querying GET /api/memory
type MemoryFilter struct {
	APIKeyID  string
	Type      string // "factual", "episodic", "procedural", "semantic"
	SessionID string
	Q         string
	Limit     int
	Page      int
	Offset    int
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

// ListMemories queries GET /api/memory on OmniRoute with optional filtering
func (c *Client) ListMemories(ctx context.Context, filter ...MemoryFilter) ([]MemoryItem, error) {
	path := "/api/memory"
	if len(filter) > 0 {
		f := filter[0]
		var params []string
		if f.SessionID != "" {
			params = append(params, fmt.Sprintf("sessionId=%s", url.QueryEscape(f.SessionID)))
		}
		if f.Type != "" {
			params = append(params, fmt.Sprintf("type=%s", url.QueryEscape(f.Type)))
		}
		if f.Q != "" {
			params = append(params, fmt.Sprintf("q=%s", url.QueryEscape(f.Q)))
		}
		if f.APIKeyID != "" {
			params = append(params, fmt.Sprintf("apiKeyId=%s", url.QueryEscape(f.APIKeyID)))
		}
		if f.Limit > 0 {
			params = append(params, fmt.Sprintf("limit=%d", f.Limit))
		}
		if f.Page > 0 {
			params = append(params, fmt.Sprintf("page=%d", f.Page))
		}
		if f.Offset > 0 {
			params = append(params, fmt.Sprintf("offset=%d", f.Offset))
		}
		if len(params) > 0 {
			path = fmt.Sprintf("%s?%s", path, strings.Join(params, "&"))
		}
	}

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list memories failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	// 1. Try decoding envelope {"data": [...]}
	var memResp MemoryListResponse
	if err := json.Unmarshal(body, &memResp); err == nil && memResp.Data != nil {
		return memResp.Data, nil
	}

	// 2. Try decoding raw array [...]
	var memList []MemoryItem
	if err := json.Unmarshal(body, &memList); err == nil {
		return memList, nil
	}

	return nil, fmt.Errorf("decode memories response failed: unexpected format")
}

// GetMemory retrieves a single memory entry by ID from OmniRoute (GET /api/memory/{id})
func (c *Client) GetMemory(ctx context.Context, id string) (*MemoryItem, error) {
	if id == "" {
		return nil, fmt.Errorf("memory id is required")
	}
	path := fmt.Sprintf("/api/memory/%s", url.PathEscape(id))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get memory failed with status %d: %s", resp.StatusCode, string(body))
	}

	var item MemoryItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, fmt.Errorf("decode memory item: %w", err)
	}
	return &item, nil
}

// CreateMemory creates a memory entry on OmniRoute (POST /api/memory)
func (c *Client) CreateMemory(ctx context.Context, key, content, memType, sessionID string, metadata map[string]interface{}) (*MemoryItem, error) {
	if memType == "" {
		memType = "factual"
	}
	payload := map[string]interface{}{
		"type":    memType,
		"key":     key,
		"content": content,
	}
	if sessionID != "" {
		payload["sessionId"] = sessionID
	}
	if metadata != nil {
		payload["metadata"] = metadata
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal memory payload: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/api/memory", data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create memory failed with status %d: %s", resp.StatusCode, string(body))
	}

	var created MemoryItem
	_ = json.NewDecoder(resp.Body).Decode(&created)
	return &created, nil
}

// UpdateMemory updates a memory entry on OmniRoute (PUT /api/memory/{id})
func (c *Client) UpdateMemory(ctx context.Context, id, key, content, memType string, metadata map[string]interface{}) error {
	if id == "" {
		return fmt.Errorf("memory id is required for update")
	}
	if memType == "" {
		memType = "factual"
	}
	payload := map[string]interface{}{
		"type":    memType,
		"key":     key,
		"content": content,
	}
	if metadata != nil {
		payload["metadata"] = metadata
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal update memory payload: %w", err)
	}

	path := fmt.Sprintf("/api/memory/%s", url.PathEscape(id))
	resp, err := c.doRequest(ctx, http.MethodPut, path, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update memory failed with status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// DeleteMemory deletes a memory entry on OmniRoute (DELETE /api/memory/{id})
func (c *Client) DeleteMemory(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("memory id is required for delete")
	}
	path := fmt.Sprintf("/api/memory/%s", url.PathEscape(id))
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete memory failed with status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// SearchMemories searches memories on OmniRoute by keyword query and optional sessionID
func (c *Client) SearchMemories(ctx context.Context, query, sessionID string) ([]MemoryItem, error) {
	return c.ListMemories(ctx, MemoryFilter{
		SessionID: sessionID,
		Q:         query,
		Limit:     100,
	})
}

// UpsertMemory creates or updates a memory entry on OmniRoute.
// If an item with the same key exists in the specified sessionID, it updates it via PUT.
// Otherwise it creates a new entry via POST.
func (c *Client) UpsertMemory(ctx context.Context, key, content, memType, sessionID string, metadata map[string]interface{}) error {
	existingList, err := c.ListMemories(ctx, MemoryFilter{
		SessionID: sessionID,
		Q:         key,
		Limit:     50,
	})
	if err == nil {
		for _, item := range existingList {
			if strings.EqualFold(item.Key, key) && (sessionID == "" || item.SessionID == sessionID) {
				return c.UpdateMemory(ctx, item.ID, key, content, memType, metadata)
			}
		}
	}
	_, err = c.CreateMemory(ctx, key, content, memType, sessionID, metadata)
	return err
}

// SaveMemory creates or updates a memory item on OmniRoute via POST /api/memory (backward-compatibility alias)
func (c *Client) SaveMemory(ctx context.Context, key, content, memType string, metadata map[string]interface{}) error {
	sessionID := ""
	if metadata != nil {
		if s, ok := metadata["sessionId"].(string); ok && s != "" {
			sessionID = s
		} else if scope, ok := metadata["scope"].(string); ok && scope != "" {
			if scopeID, ok := metadata["scope_id"].(string); ok && scopeID != "" {
				sessionID = fmt.Sprintf("%s:%s", scope, scopeID)
			}
		}
	}
	return c.UpsertMemory(ctx, key, content, memType, sessionID, metadata)
}
