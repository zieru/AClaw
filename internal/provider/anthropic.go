package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"goassistant/internal/tools"
)

// AnthropicProvider handles Anthropic Claude messages API
type AnthropicProvider struct {
	name         string
	keyPool      *KeyPool
	defaultModel string
	models       []string
	client       *http.Client
}

func NewAnthropicProvider(name, apiKey, defaultModel string) *AnthropicProvider {
	var keys []string
	if apiKey != "" {
		keys = []string{apiKey}
	}
	return NewAnthropicProviderWithKeys(name, keys, "round-robin", defaultModel, nil)
}

func NewAnthropicProviderWithKeys(name string, keys []string, keyStrategy string, defaultModel string, models []string) *AnthropicProvider {
	if defaultModel == "" {
		defaultModel = "claude-3-5-sonnet-20241022"
	}
	if len(models) == 0 {
		models = []string{"claude-3-5-sonnet-20241022", "claude-3-5-haiku-20241022", "claude-3-opus-20240229"}
	}
	return &AnthropicProvider{
		name:         name,
		keyPool:      NewKeyPool(keys, keyStrategy),
		defaultModel: defaultModel,
		models:       models,
		client:       &http.Client{Timeout: defaultAPITimeout()},
	}
}

func (p *AnthropicProvider) Name() string         { return p.name }
func (p *AnthropicProvider) Type() string         { return "anthropic" }
func (p *AnthropicProvider) DefaultModel() string { return p.defaultModel }
func (p *AnthropicProvider) Models() []string     { return p.models }
func (p *AnthropicProvider) KeyPool() *KeyPool    { return p.keyPool }

func (p *AnthropicProvider) SetHTTPClient(client interface{}) {
	if c, ok := client.(*http.Client); ok && c != nil {
		p.client = c
	}
}

type anthropicToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema tools.ParametersSchema `json:"input_schema"`
}

type anthropicContentBlock struct {
	Type      string                 `json:"type"` // text, tool_use, tool_result, thinking
	Text      string                 `json:"text,omitempty"`
	Thinking  string                 `json:"thinking,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Input     map[string]interface{} `json:"input,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
	Content   string                 `json:"content,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicReqBody struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicToolDef `json:"tools,omitempty"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
}

type anthropicRespBody struct {
	ID      string                  `json:"id"`
	Type    string                  `json:"type"`
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
	Model   string                  `json:"model"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *AnthropicProvider) GenerateChat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	start := time.Now()
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	var systemPrompt string
	var messages []anthropicMessage

	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			systemPrompt = m.Content
			continue
		}

		var blocks []anthropicContentBlock
		if m.Role == RoleTool {
			blocks = append(blocks, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			})
			messages = append(messages, anthropicMessage{
				Role:    "user",
				Content: blocks,
			})
			continue
		}

		if m.Content != "" {
			blocks = append(blocks, anthropicContentBlock{
				Type: "text",
				Text: m.Content,
			})
		}

		for _, tc := range m.ToolCalls {
			blocks = append(blocks, anthropicContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Name,
				Input: tc.Arguments,
			})
		}

		role := "user"
		if m.Role == RoleAssistant {
			role = "assistant"
		}

		if len(blocks) > 0 {
			messages = append(messages, anthropicMessage{
				Role:    role,
				Content: blocks,
			})
		}
	}

	var toolDefs []anthropicToolDef
	for _, t := range req.Tools {
		toolDefs = append(toolDefs, anthropicToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Parameters(),
		})
	}

	payload := anthropicReqBody{
		Model:       model,
		System:      systemPrompt,
		Messages:    messages,
		Tools:       toolDefs,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal anthropic payload: %w", err)
	}

	keyCount := p.keyPool.Count()
	maxAttempts := keyCount
	if maxAttempts < 2 {
		maxAttempts = 2
	}

	var lastErr error
	var respBytes []byte

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		apiKey := p.keyPool.GetNextKey()

		// Per-attempt timeout to prevent single key from hanging the entire request
		attemptTimeout := 35 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining > 0 && remaining < attemptTimeout {
				attemptTimeout = remaining
			}
		}
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, attemptTimeout)

		httpReq, err := http.NewRequestWithContext(attemptCtx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(payloadBytes))
		if err != nil {
			cancelAttempt()
			return nil, err
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("anthropic-version", "2023-06-01")
		if apiKey != "" {
			httpReq.Header.Set("x-api-key", apiKey)
		}

		httpResp, err := p.client.Do(httpReq)
		if err != nil {
			cancelAttempt()
			if apiKey != "" {
				if attemptCtx.Err() == context.DeadlineExceeded || ctx.Err() != nil {
					p.keyPool.MarkTimeout(apiKey)
				} else {
					p.keyPool.MarkError(apiKey, false)
				}
			}
			lastErr = fmt.Errorf("anthropic api call failed: %w", err)
			continue
		}

		respBytes, err = io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		cancelAttempt()

		if err != nil {
			lastErr = fmt.Errorf("failed to read response: %w", err)
			continue
		}

		if httpResp.StatusCode == 429 {
			if apiKey != "" {
				p.keyPool.MarkRateLimit(apiKey)
			}
			lastErr = fmt.Errorf("anthropic rate limit error (429): %s", string(respBytes))
			continue // retry with next key
		}

		if httpResp.StatusCode == 401 || httpResp.StatusCode == 403 {
			if apiKey != "" {
				p.keyPool.MarkAuthError(apiKey)
			}
			lastErr = fmt.Errorf("anthropic auth error (%d): %s", httpResp.StatusCode, string(respBytes))
			continue // retry with next key
		}

		if httpResp.StatusCode >= 500 {
			if apiKey != "" {
				p.keyPool.MarkTimeout(apiKey)
			}
			lastErr = fmt.Errorf("anthropic server error (%d): %s", httpResp.StatusCode, string(respBytes))
			continue // retry with next key
		}

		if httpResp.StatusCode >= 400 {
			return nil, fmt.Errorf("anthropic api error (%d): %s", httpResp.StatusCode, string(respBytes))
		}

		if apiKey != "" {
			p.keyPool.MarkSuccess(apiKey)
		}
		lastErr = nil
		break
	}

	if lastErr != nil {
		return nil, lastErr
	}

	var respBody anthropicRespBody
	if err := json.Unmarshal(respBytes, &respBody); err != nil {
		return nil, fmt.Errorf("failed to unmarshal anthropic response: %w", err)
	}

	if respBody.Error != nil {
		return nil, fmt.Errorf("anthropic error: %s", respBody.Error.Message)
	}

	var textParts []string
	var thinkingParts []string
	var toolCalls []ToolCall

	for _, block := range respBody.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "thinking":
			if block.Thinking != "" {
				thinkingParts = append(thinkingParts, block.Thinking)
			} else if block.Text != "" {
				thinkingParts = append(thinkingParts, block.Text)
			}
		case "tool_use":
			toolCalls = append(toolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}

	totalTokens := respBody.Usage.InputTokens + respBody.Usage.OutputTokens
	cost := (float64(respBody.Usage.InputTokens)*0.003 + float64(respBody.Usage.OutputTokens)*0.015) / 1000.0

	actualModel := respBody.Model
	if actualModel == "" {
		actualModel = model
	}

	return &ChatResponse{
		Content:          strings.Join(textParts, "\n"),
		Thinking:         strings.Join(thinkingParts, "\n"),
		ToolCalls:        toolCalls,
		PromptTokens:     respBody.Usage.InputTokens,
		CompletionTokens: respBody.Usage.OutputTokens,
		TotalTokens:      totalTokens,
		CostUSD:          cost,
		Latency:          time.Since(start),
		Model:            actualModel,
		ProviderName:     p.name,
	}, nil
}

// GenerateChatStream implements StreamingProvider for Anthropic using messages?stream=true
func (p *AnthropicProvider) GenerateChatStream(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	start := time.Now()
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	var systemPrompt string
	var messages []anthropicMessage

	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			systemPrompt = m.Content
			continue
		}
		if m.Role == RoleTool {
			messages = append(messages, anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}},
			})
			continue
		}
		var blocks []anthropicContentBlock
		if m.Content != "" {
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, anthropicContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: tc.Arguments})
		}
		role := "user"
		if m.Role == RoleAssistant {
			role = "assistant"
		}
		if len(blocks) > 0 {
			messages = append(messages, anthropicMessage{Role: role, Content: blocks})
		}
	}

	var toolDefs []anthropicToolDef
	for _, t := range req.Tools {
		toolDefs = append(toolDefs, anthropicToolDef{
			Name: t.Name(), Description: t.Description(), InputSchema: t.Parameters(),
		})
	}

	payload := anthropicReqBody{
		Model:       model,
		System:      systemPrompt,
		Messages:    messages,
		Tools:       toolDefs,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal stream request: %w", err)
	}

	keyCount := p.keyPool.Count()
	maxAttempts := keyCount
	if maxAttempts < 2 {
		maxAttempts = 2
	}

	var lastErr error
	var resp *http.Response
	var apiKey string

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		apiKey = p.keyPool.GetNextKey()
		url := "https://api.anthropic.com/v1/messages?stream=true"
		httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payloadBytes))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("anthropic-version", "2023-06-01")
		if apiKey != "" {
			httpReq.Header.Set("x-api-key", apiKey)
		}

		resp, err = p.client.Do(httpReq)
		if err != nil {
			if apiKey != "" {
				p.keyPool.MarkTimeout(apiKey)
			}
			lastErr = fmt.Errorf("anthropic stream request failed: %w", err)
			continue
		}

		if resp.StatusCode == 429 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if apiKey != "" {
				p.keyPool.MarkRateLimit(apiKey)
			}
			lastErr = fmt.Errorf("anthropic stream rate limit error (429): %s", string(bodyBytes))
			continue
		}

		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if apiKey != "" {
				p.keyPool.MarkAuthError(apiKey)
			}
			lastErr = fmt.Errorf("anthropic stream auth error (%d): %s", resp.StatusCode, string(bodyBytes))
			continue
		}

		if resp.StatusCode >= 500 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if apiKey != "" {
				p.keyPool.MarkTimeout(apiKey)
			}
			lastErr = fmt.Errorf("anthropic stream server error (%d): %s", resp.StatusCode, string(bodyBytes))
			continue
		}

		if resp.StatusCode >= 400 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("anthropic stream api error (%d): %s", resp.StatusCode, string(bodyBytes))
		}

		if apiKey != "" {
			p.keyPool.MarkSuccess(apiKey)
		}
		lastErr = nil
		break
	}

	if lastErr != nil {
		return nil, lastErr
	}
	defer resp.Body.Close()

	var contentBuilder strings.Builder
	var thinkingBuilder strings.Builder
	var toolCalls []ToolCall
	var promptTokens, outputTokens, totalTokens int

	buf := make([]byte, 4096)
	var lineBuffer strings.Builder

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			lineBuffer.Write(buf[:n])
			text := lineBuffer.String()
			lines := strings.Split(text, "\n")
			if !strings.HasSuffix(text, "\n") && len(lines) > 0 {
				lineBuffer.Reset()
				lineBuffer.WriteString(lines[len(lines)-1])
				lines = lines[:len(lines)-1]
			} else {
				lineBuffer.Reset()
			}

			for _, line := range lines {
				line = strings.TrimSpace(line)
				if len(line) < 6 || line[:5] != "data:" {
					continue
				}
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if data == "" || data == "[DONE]" {
					continue
				}

				var streamEvent map[string]interface{}
				if err := json.Unmarshal([]byte(data), &streamEvent); err != nil {
					continue
				}

				eventType, _ := streamEvent["type"].(string)

				switch eventType {
				case "content_block_delta":
					deltaType, _ := streamEvent["delta"].(map[string]interface{})
					if deltaType == nil {
						continue
					}
					dt, _ := deltaType["type"].(string)
					switch dt {
					case "text_delta":
						if t, ok := deltaType["text"].(string); ok && t != "" {
							contentBuilder.WriteString(t)
							if req.StreamCallback != nil {
								req.StreamCallback(StreamChunk{Content: t})
							}
						}
					case "thinking_delta":
						if t, ok := deltaType["thinking"].(string); ok && t != "" {
							thinkingBuilder.WriteString(t)
							if req.StreamCallback != nil {
								req.StreamCallback(StreamChunk{Thinking: t})
							}
						}
					}
				case "content_block_start":
					contentBlock, _ := streamEvent["content_block"].(map[string]interface{})
					if contentBlock == nil {
						continue
					}
					cbType, _ := contentBlock["type"].(string)
					if cbType == "thinking" {
						if t, ok := contentBlock["thinking"].(string); ok && t != "" {
							thinkingBuilder.WriteString(t)
							if req.StreamCallback != nil {
								req.StreamCallback(StreamChunk{Thinking: t})
							}
						}
					} else if cbType == "tool_use" {
						var args map[string]interface{}
						if rawArgs, ok := contentBlock["input"].(map[string]interface{}); ok {
							for k, v := range rawArgs {
								args[k] = v
							}
						}
						toolCalls = append(toolCalls, ToolCall{
							ID:        fmt.Sprintf("%v", contentBlock["id"]),
							Name:      fmt.Sprintf("%v", contentBlock["name"]),
							Arguments: args,
						})
					} else if cbType == "text" {
						if t, ok := contentBlock["text"].(string); ok && t != "" {
							contentBuilder.WriteString(t)
							if req.StreamCallback != nil {
								req.StreamCallback(StreamChunk{Content: t})
							}
						}
					}

				case "message_delta":
					usage, _ := streamEvent["usage"].(map[string]interface{})
					if usage != nil {
						if ot, ok := usage["output_tokens"].(float64); ok {
							outputTokens = int(ot)
						}
					}

				case "message_stop":
					if req.StreamCallback != nil {
						req.StreamCallback(StreamChunk{Done: true})
					}
				}
			}
		}
		if readErr != nil {
			break
		}
	}

	if req.StreamCallback != nil {
		req.StreamCallback(StreamChunk{Done: true})
	}

	totalTokens = promptTokens + outputTokens
	if totalTokens == 0 {
		totalTokens = (contentBuilder.Len() + thinkingBuilder.Len()) / 4
	}

	parsedToolCalls := make([]ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if tc.Arguments == nil {
			tc.Arguments = map[string]interface{}{}
		}
		parsedToolCalls = append(parsedToolCalls, tc)
	}

	cost := (float64(promptTokens)*0.003 + float64(outputTokens)*0.015) / 1000.0

	return &ChatResponse{
		Content:          contentBuilder.String(),
		Thinking:         thinkingBuilder.String(),
		ToolCalls:        parsedToolCalls,
		PromptTokens:     promptTokens,
		CompletionTokens: outputTokens,
		TotalTokens:      totalTokens,
		CostUSD:          cost,
		Latency:          time.Since(start),
		Model:            model,
		ProviderName:     p.name,
	}, nil
}

// Ensure AnthropicProvider implements StreamingProvider
var _ StreamingProvider = (*AnthropicProvider)(nil)
