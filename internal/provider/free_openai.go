package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
)

// FreeEndpoint represents a single free AI endpoint with health tracking
type FreeEndpoint struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	BaseURL         string    `json:"base_url"`
	APIKey          string    `json:"api_key,omitempty"`         // Some free endpoints need a free API key
	AuthHeader      string    `json:"auth_header,omitempty"`     // Custom auth header (default: Authorization: Bearer)
	DefaultModel    string    `json:"default_model"`
	Models          []string  `json:"models"`
	IsActive        bool      `json:"is_active"`
	Priority        int       `json:"priority"`                  // Lower = higher priority
	MaxRPM          int       `json:"max_rpm"`                   // Rate limit: max requests per minute (0 = unlimited)
	SupportsTools   bool      `json:"supports_tools"`
	SupportsStream  bool      `json:"supports_stream"`

	// Health tracking (runtime, not persisted)
	mu               sync.Mutex
	consecutiveFails int
	totalRequests    int
	totalSuccess     int
	totalFails       int
	lastRequestAt    time.Time
	cooldownUntil    time.Time
	avgLatencyMs     int64
}

// IsHealthy returns whether this endpoint can accept requests
func (e *FreeEndpoint) IsHealthy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.IsActive {
		return false
	}

	now := time.Now()

	// Check cooldown
	if now.Before(e.cooldownUntil) {
		return false
	}

	// Check RPM throttle
	if e.MaxRPM > 0 {
		minInterval := time.Minute / time.Duration(e.MaxRPM)
		if now.Sub(e.lastRequestAt) < minInterval {
			return false
		}
	}

	return true
}

// MarkRequest records that a request is being made
func (e *FreeEndpoint) MarkRequest() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastRequestAt = time.Now()
	e.totalRequests++
}

// MarkSuccess records a successful request
func (e *FreeEndpoint) MarkSuccess(latencyMs int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.consecutiveFails = 0
	e.totalSuccess++
	if e.avgLatencyMs == 0 {
		e.avgLatencyMs = latencyMs
	} else {
		e.avgLatencyMs = (e.avgLatencyMs*3 + latencyMs) / 4 // Exponential moving avg
	}
}

// MarkFail records a failed request and applies cooldown
func (e *FreeEndpoint) MarkFail(isRateLimit bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.consecutiveFails++
	e.totalFails++

	// Exponential backoff: 15s, 30s, 60s, 120s, 240s (max)
	baseSec := 15
	if isRateLimit {
		baseSec = 30 // Longer base for rate limits
	}
	backoffSec := baseSec * (1 << (e.consecutiveFails - 1))
	if backoffSec > 240 {
		backoffSec = 240
	}
	e.cooldownUntil = time.Now().Add(time.Duration(backoffSec) * time.Second)
}

// HealthScore returns a score 0-100 for routing decisions (higher = better)
func (e *FreeEndpoint) HealthScore() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.IsActive {
		return 0
	}

	score := 100

	// Penalize consecutive failures
	score -= e.consecutiveFails * 20

	// Penalize high latency (>5s)
	if e.avgLatencyMs > 5000 {
		score -= 15
	} else if e.avgLatencyMs > 10000 {
		score -= 30
	}

	// Bonus for high success rate
	if e.totalRequests > 5 {
		successRate := float64(e.totalSuccess) / float64(e.totalRequests)
		if successRate > 0.9 {
			score += 10
		} else if successRate < 0.5 {
			score -= 20
		}
	}

	// Apply priority offset
	score -= e.Priority * 5

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score
}

// ============================================================================
// FreeRouterProvider — Multi-Endpoint Free AI Router (like 9Router for free)
// ============================================================================

// FreeRouterProvider manages multiple free AI endpoints with smart routing,
// health-based failover, rate limiting, and auto-discovery.
// Mimics the behavior of 9Router/OmniRouter but for free/community endpoints.
type FreeRouterProvider struct {
	name         string
	providerType string
	defaultModel string

	mu        sync.RWMutex
	endpoints []*FreeEndpoint
	client    *http.Client
	strategy  string // "smart" (health-score), "round-robin", "random", "failover"
	rrIndex   int    // For round-robin
}

// NewFreeRouterProvider creates a new Free Router with built-in known endpoints
func NewFreeRouterProvider(name, providerType string, customEndpoints []*FreeEndpoint, strategy string) *FreeRouterProvider {
	if strategy == "" {
		strategy = "smart"
	}
	if name == "" {
		name = "FreeRouter"
	}
	if providerType == "" {
		providerType = "free_router"
	}

	p := &FreeRouterProvider{
		name:         name,
		providerType: providerType,
		defaultModel: "gpt-4o-mini",
		client:       &http.Client{Timeout: time.Duration(config.Get().Timeouts.APICallSeconds) * time.Second},
		strategy:     strategy,
	}

	// Seed built-in known free endpoints
	p.endpoints = getBuiltInFreeEndpoints()

	// Merge custom endpoints
	for _, ep := range customEndpoints {
		p.AddEndpoint(ep)
	}

	return p
}

// NewFreeOpenAIProvider creates a backward-compatible free provider (single endpoint)
func NewFreeOpenAIProvider(name, providerType, baseURL, apiKey, defaultModel string) *FreeRouterProvider {
	ep := &FreeEndpoint{
		ID:            fmt.Sprintf("custom_%s", strings.ToLower(name)),
		Name:          name,
		BaseURL:       baseURL,
		APIKey:        apiKey,
		DefaultModel:  defaultModel,
		Models:        []string{defaultModel},
		IsActive:      true,
		Priority:      0,
		MaxRPM:        20,
		SupportsTools: true,
	}
	return NewFreeRouterProvider(name, providerType, []*FreeEndpoint{ep}, "failover")
}

// NewFreeOpenAIProviderWithKeys creates a backward-compatible provider with key pool
func NewFreeOpenAIProviderWithKeys(name, providerType, baseURL string, keys []string, keyStrategy string, defaultModel string, models []string) *FreeRouterProvider {
	if len(models) == 0 && defaultModel != "" {
		models = []string{defaultModel}
	}

	// Create one endpoint per key, or a single keyless endpoint
	var endpoints []*FreeEndpoint
	if len(keys) == 0 {
		endpoints = append(endpoints, &FreeEndpoint{
			ID:            fmt.Sprintf("custom_%s", strings.ToLower(name)),
			Name:          name,
			BaseURL:       baseURL,
			DefaultModel:  defaultModel,
			Models:        models,
			IsActive:      true,
			Priority:      0,
			MaxRPM:        20,
			SupportsTools: true,
		})
	} else {
		for i, key := range keys {
			endpoints = append(endpoints, &FreeEndpoint{
				ID:            fmt.Sprintf("custom_%s_%d", strings.ToLower(name), i),
				Name:          fmt.Sprintf("%s (Key %d)", name, i+1),
				BaseURL:       baseURL,
				APIKey:        key,
				DefaultModel:  defaultModel,
				Models:        models,
				IsActive:      true,
				Priority:      0,
				MaxRPM:        30,
				SupportsTools: true,
			})
		}
	}

	strategy := "smart"
	if keyStrategy == "failover" {
		strategy = "failover"
	}
	return NewFreeRouterProvider(name, providerType, endpoints, strategy)
}

func (p *FreeRouterProvider) Name() string         { return p.name }
func (p *FreeRouterProvider) Type() string         { return p.providerType }
func (p *FreeRouterProvider) DefaultModel() string { return p.defaultModel }

func (p *FreeRouterProvider) Models() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	seen := make(map[string]bool)
	var all []string
	for _, ep := range p.endpoints {
		if !ep.IsActive {
			continue
		}
		for _, m := range ep.Models {
			if !seen[m] {
				seen[m] = true
				all = append(all, m)
			}
		}
	}
	return all
}

func (p *FreeRouterProvider) SetHTTPClient(client interface{}) {
	if c, ok := client.(*http.Client); ok && c != nil {
		p.client = c
	}
}

// AddEndpoint adds or updates a free endpoint
func (p *FreeRouterProvider) AddEndpoint(ep *FreeEndpoint) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Update existing if same ID
	for i, existing := range p.endpoints {
		if existing.ID == ep.ID {
			p.endpoints[i] = ep
			return
		}
	}
	p.endpoints = append(p.endpoints, ep)
}

// RemoveEndpoint removes a free endpoint by ID
func (p *FreeRouterProvider) RemoveEndpoint(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var filtered []*FreeEndpoint
	for _, ep := range p.endpoints {
		if ep.ID != id {
			filtered = append(filtered, ep)
		}
	}
	p.endpoints = filtered
}

// ListEndpoints returns all endpoints with their health info
func (p *FreeRouterProvider) ListEndpoints() []*FreeEndpoint {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]*FreeEndpoint, len(p.endpoints))
	copy(result, p.endpoints)
	return result
}

// selectEndpoints returns prioritized endpoints that support the requested model
func (p *FreeRouterProvider) selectEndpoints(model string, needTools bool) []*FreeEndpoint {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var candidates []*FreeEndpoint
	for _, ep := range p.endpoints {
		if !ep.IsHealthy() {
			continue
		}
		if needTools && !ep.SupportsTools {
			continue
		}

		// Check if endpoint supports the requested model
		if model != "" {
			supportsModel := false
			for _, m := range ep.Models {
				if strings.EqualFold(m, model) {
					supportsModel = true
					break
				}
			}
			// Also match if endpoint's default model matches
			if strings.EqualFold(ep.DefaultModel, model) {
				supportsModel = true
			}
			// If model not explicitly listed, still try (many free endpoints proxy multiple models)
			if !supportsModel && len(ep.Models) > 0 {
				// Skip endpoints that have explicit model lists and don't include this model
				continue
			}
		}

		candidates = append(candidates, ep)
	}

	// If no candidates with matching model, fall back to all healthy endpoints
	if len(candidates) == 0 {
		for _, ep := range p.endpoints {
			if ep.IsHealthy() && (!needTools || ep.SupportsTools) {
				candidates = append(candidates, ep)
			}
		}
	}

	// If still none (all in cooldown), use all active endpoints ignoring cooldown
	if len(candidates) == 0 {
		for _, ep := range p.endpoints {
			if ep.IsActive && (!needTools || ep.SupportsTools) {
				candidates = append(candidates, ep)
			}
		}
	}

	// Sort by strategy
	switch p.strategy {
	case "smart":
		// Sort by health score (descending)
		for i := 0; i < len(candidates); i++ {
			for j := i + 1; j < len(candidates); j++ {
				if candidates[j].HealthScore() > candidates[i].HealthScore() {
					candidates[i], candidates[j] = candidates[j], candidates[i]
				}
			}
		}
	case "random":
		rand.Shuffle(len(candidates), func(i, j int) {
			candidates[i], candidates[j] = candidates[j], candidates[i]
		})
	case "round-robin":
		if len(candidates) > 0 {
			p.mu.RUnlock()
			p.mu.Lock()
			p.rrIndex = (p.rrIndex + 1) % len(candidates)
			idx := p.rrIndex
			p.mu.Unlock()
			p.mu.RLock()
			// Rotate candidates to start from rrIndex
			rotated := make([]*FreeEndpoint, len(candidates))
			for i := range candidates {
				rotated[i] = candidates[(idx+i)%len(candidates)]
			}
			candidates = rotated
		}
	// "failover": already in order (first healthy wins)
	}

	return candidates
}

func (p *FreeRouterProvider) GenerateChat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	start := time.Now()
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	needTools := len(req.Tools) > 0
	candidates := p.selectEndpoints(model, needTools)

	if len(candidates) == 0 {
		return nil, fmt.Errorf("FreeRouter: tidak ada endpoint free yang tersedia atau sehat")
	}

	// Build messages once
	var msgs []openAIMessage
	for _, m := range req.Messages {
		var toolCalls []openAIToolCall
		for _, tc := range m.ToolCalls {
			argBytes, _ := json.Marshal(tc.Arguments)
			toolCalls = append(toolCalls, openAIToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      tc.Name,
					Arguments: string(argBytes),
				},
			})
		}
		msgs = append(msgs, openAIMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
			ToolCalls:  toolCalls,
		})
	}

	var toolDefs []openAIToolDef
	for _, t := range req.Tools {
		toolDefs = append(toolDefs, openAIToolDef{
			Type: "function",
			Function: openAIFunctionDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}

	// Use endpoint's model if our model isn't supported
	useModel := model

	var lastErr error

	// Try each candidate endpoint with failover
	for _, ep := range candidates {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Resolve model: if endpoint doesn't list the model, use its default
		epModel := useModel
		modelFound := false
		for _, m := range ep.Models {
			if strings.EqualFold(m, useModel) {
				modelFound = true
				break
			}
		}
		if !modelFound && ep.DefaultModel != "" {
			epModel = ep.DefaultModel
		}

		resp, err := p.callEndpoint(ctx, ep, epModel, msgs, toolDefs, req)
		if err == nil && resp != nil {
			resp.Latency = time.Since(start)
			resp.ProviderName = fmt.Sprintf("%s→%s", p.name, ep.Name)
			return resp, nil
		}

		lastErr = fmt.Errorf("[%s] %v", ep.Name, err)
	}

	return nil, fmt.Errorf("FreeRouter: semua %d endpoint gagal: %w", len(candidates), lastErr)
}

// callEndpoint executes a single chat request against one free endpoint
func (p *FreeRouterProvider) callEndpoint(ctx context.Context, ep *FreeEndpoint, model string, msgs []openAIMessage, toolDefs []openAIToolDef, req ChatRequest) (*ChatResponse, error) {
	callStart := time.Now()

	ep.MarkRequest()

	baseURL := strings.TrimSuffix(ep.BaseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/chat/completions")
	baseURL = strings.TrimSuffix(baseURL, "/chat")
	baseURL = strings.TrimSuffix(baseURL, "/completions")

	// Build payload
	reqPayload := openAIReqBody{
		Model:       model,
		Messages:    msgs,
		Tools:       toolDefs,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      false,
	}

	payloadBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}

	endpoint := fmt.Sprintf("%s/chat/completions", baseURL)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "GoAssistant/1.2")

	// Set auth header
	if ep.APIKey != "" {
		if ep.AuthHeader != "" {
			httpReq.Header.Set(ep.AuthHeader, ep.APIKey)
		} else {
			httpReq.Header.Set("Authorization", "Bearer "+ep.APIKey)
		}
	}

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		ep.MarkFail(false)
		return nil, fmt.Errorf("http error: %w", err)
	}

	bodyBytes, err := io.ReadAll(httpResp.Body)
	httpResp.Body.Close()
	if err != nil {
		ep.MarkFail(false)
		return nil, fmt.Errorf("read error: %w", err)
	}

	if httpResp.StatusCode == 429 {
		ep.MarkFail(true)
		return nil, fmt.Errorf("rate limited (429)")
	}

	if httpResp.StatusCode == 401 || httpResp.StatusCode == 403 {
		ep.MarkFail(false)
		return nil, fmt.Errorf("auth error (%d)", httpResp.StatusCode)
	}

	if httpResp.StatusCode >= 500 {
		ep.MarkFail(false)
		return nil, fmt.Errorf("server error (%d)", httpResp.StatusCode)
	}

	if httpResp.StatusCode >= 400 {
		ep.MarkFail(false)
		preview := string(bodyBytes)
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return nil, fmt.Errorf("api error (%d): %s", httpResp.StatusCode, preview)
	}

	latencyMs := time.Since(callStart).Milliseconds()

	// Parse response
	var respBody openAIRespBody
	if err := json.Unmarshal(bodyBytes, &respBody); err == nil {
		if respBody.Error != nil {
			ep.MarkFail(false)
			return nil, fmt.Errorf("api error: %s", respBody.Error.Message)
		}

		if len(respBody.Choices) > 0 {
			ep.MarkSuccess(latencyMs)
			choice := respBody.Choices[0]

			var parsedToolCalls []ToolCall
			for _, tc := range choice.Message.ToolCalls {
				var args map[string]interface{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				parsedToolCalls = append(parsedToolCalls, ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: args,
				})
			}

			actualModel := respBody.Model
			if actualModel == "" {
				actualModel = model
			}

			return &ChatResponse{
				Content:          choice.Message.Content,
				Thinking:         choice.Message.ReasoningContent,
				ToolCalls:        parsedToolCalls,
				PromptTokens:     respBody.Usage.PromptTokens,
				CompletionTokens: respBody.Usage.CompletionTokens,
				TotalTokens:      respBody.Usage.TotalTokens,
				CostUSD:          0.0, // Free!
				Model:            actualModel,
				ProviderName:     ep.Name,
			}, nil
		}
	}

	// Fallback: try SSE parsing
	bodyStr := string(bodyBytes)
	if strings.Contains(bodyStr, "data:") {
		var combinedContent strings.Builder
		var totalTokens int
		actualModel := model
		lines := strings.Split(bodyStr, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			chunkJSON := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if chunkJSON == "[DONE]" || chunkJSON == "" {
				continue
			}

			var chunk openAIRespBody
			if err := json.Unmarshal([]byte(chunkJSON), &chunk); err == nil {
				if chunk.Model != "" {
					actualModel = chunk.Model
				}
				if len(chunk.Choices) > 0 {
					if chunk.Choices[0].Delta.Content != "" {
						combinedContent.WriteString(chunk.Choices[0].Delta.Content)
					} else if chunk.Choices[0].Message.Content != "" {
						combinedContent.WriteString(chunk.Choices[0].Message.Content)
					}
				}
				if chunk.Usage.TotalTokens > 0 {
					totalTokens = chunk.Usage.TotalTokens
				}
			}
		}

		if combinedContent.Len() > 0 {
			ep.MarkSuccess(latencyMs)
			if totalTokens == 0 {
				totalTokens = combinedContent.Len() / 4
			}
			return &ChatResponse{
				Content:      combinedContent.String(),
				TotalTokens:  totalTokens,
				CostUSD:      0.0,
				Model:        actualModel,
				ProviderName: ep.Name,
			}, nil
		}
	}

	ep.MarkFail(false)
	preview := bodyStr
	if len(preview) > 200 {
		preview = preview[:200]
	}
	return nil, fmt.Errorf("unparseable response: %s", preview)
}

// ============================================================================
// Built-in Known Free Endpoints Registry
// ============================================================================

func getBuiltInFreeEndpoints() []*FreeEndpoint {
	return []*FreeEndpoint{
		// --- Google AI Studio (Free Tier — needs free API key from aistudio.google.com) ---
		{
			ID:            "gemini_free",
			Name:          "Google Gemini (Free API)",
			BaseURL:       "https://generativelanguage.googleapis.com/v1beta/openai",
			DefaultModel:  "gemini-2.0-flash",
			Models:        []string{"gemini-2.0-flash", "gemini-2.0-flash-lite", "gemini-1.5-flash", "gemini-1.5-pro"},
			IsActive:      false, // User needs to add their free API key to activate
			Priority:      1,
			MaxRPM:        15, // Free tier: 15 RPM
			SupportsTools: true,
			SupportsStream: true,
		},
		// --- Community Free Endpoints (OpenAI-compatible format) ---
		{
			ID:            "opencodefree",
			Name:          "OpenCodeFree",
			BaseURL:       "https://api.opencodefree.com/v1",
			DefaultModel:  "gpt-4o-mini",
			Models:        []string{"gpt-4o-mini", "gpt-4o", "gpt-3.5-turbo"},
			IsActive:      false, // Activate when URL is verified
			Priority:      3,
			MaxRPM:        10,
			SupportsTools: true,
		},
		// --- Local/Self-Hosted Endpoints ---
		{
			ID:            "ollama_local",
			Name:          "Ollama (Local)",
			BaseURL:       "http://localhost:11434/v1",
			DefaultModel:  "llama3",
			Models:        []string{"llama3", "mistral", "codellama", "phi3"},
			IsActive:      false, // Activate if Ollama is running
			Priority:      0,     // Highest priority — local is fastest
			MaxRPM:        0,     // No rate limit for local
			SupportsTools: false,
			SupportsStream: true,
		},
		{
			ID:            "lmstudio_local",
			Name:          "LM Studio (Local)",
			BaseURL:       "http://localhost:1234/v1",
			DefaultModel:  "local-model",
			Models:        []string{"local-model"},
			IsActive:      false,
			Priority:      0,
			MaxRPM:        0,
			SupportsTools: true,
			SupportsStream: true,
		},
	}
}

// GetEndpointStats returns formatted stats for admin display
func (p *FreeRouterProvider) GetEndpointStats() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🆓 <b>FreeRouter: %s</b> (Strategi: %s)\n", p.name, p.strategy))
	sb.WriteString(fmt.Sprintf("📊 Total Endpoints: %d\n\n", len(p.endpoints)))

	for i, ep := range p.endpoints {
		ep.mu.Lock()
		status := "🟢"
		if !ep.IsActive {
			status = "⚫"
		} else if time.Now().Before(ep.cooldownUntil) {
			status = "🔴"
		} else if ep.consecutiveFails > 0 {
			status = "🟡"
		}

		successRate := 0.0
		if ep.totalRequests > 0 {
			successRate = float64(ep.totalSuccess) / float64(ep.totalRequests) * 100
		}

		sb.WriteString(fmt.Sprintf("%d. %s <b>%s</b>\n", i+1, status, ep.Name))
		sb.WriteString(fmt.Sprintf("   URL: <code>%s</code>\n", ep.BaseURL))
		sb.WriteString(fmt.Sprintf("   Model: %s | RPM: %d\n", ep.DefaultModel, ep.MaxRPM))
		sb.WriteString(fmt.Sprintf("   Score: %d | Req: %d | OK: %.0f%% | Latency: %dms\n",
			ep.HealthScore(), ep.totalRequests, successRate, ep.avgLatencyMs))

		if ep.consecutiveFails > 0 {
			sb.WriteString(fmt.Sprintf("   ⚠️ Fails beruntun: %d\n", ep.consecutiveFails))
		}
		if time.Now().Before(ep.cooldownUntil) {
			remaining := time.Until(ep.cooldownUntil).Seconds()
			sb.WriteString(fmt.Sprintf("   ❄️ Cooldown: %.0fs tersisa\n", remaining))
		}
		ep.mu.Unlock()
		sb.WriteString("\n")
	}

	return sb.String()
}

// Ensure FreeRouterProvider implements Provider
var _ Provider = (*FreeRouterProvider)(nil)
