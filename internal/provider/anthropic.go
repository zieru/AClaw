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
		client:       &http.Client{Timeout: 90 * time.Second},
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
	Type      string                 `json:"type"` // text, tool_use, tool_result
	Text      string                 `json:"text,omitempty"`
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
	if keyCount == 0 {
		keyCount = 1
	}

	var lastErr error
	var respBytes []byte

	for attempt := 0; attempt < keyCount; attempt++ {
		apiKey := p.keyPool.GetNextKey()

		httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(payloadBytes))
		if err != nil {
			return nil, err
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("anthropic-version", "2023-06-01")
		if apiKey != "" {
			httpReq.Header.Set("x-api-key", apiKey)
		}

		httpResp, err := p.client.Do(httpReq)
		if err != nil {
			if apiKey != "" {
				p.keyPool.MarkError(apiKey, false)
			}
			lastErr = fmt.Errorf("anthropic api call failed: %w", err)
			continue
		}

		respBytes, err = io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read response: %w", err)
			continue
		}

		if httpResp.StatusCode == 429 || httpResp.StatusCode == 401 || httpResp.StatusCode == 403 {
			if apiKey != "" {
				p.keyPool.MarkError(apiKey, httpResp.StatusCode == 429)
			}
			lastErr = fmt.Errorf("anthropic api error (%d): %s", httpResp.StatusCode, string(respBytes))
			if attempt < keyCount-1 {
				continue // retry with next key
			}
			return nil, lastErr
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
	var toolCalls []ToolCall

	for _, block := range respBody.Content {
		if block.Type == "text" {
			textParts = append(textParts, block.Text)
		} else if block.Type == "tool_use" {
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
