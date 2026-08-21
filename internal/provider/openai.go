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

// OpenAIProvider handles OpenAI, 9Router, and all OpenAI-compatible endpoints (Groq, DeepSeek, Ollama, etc.)
type OpenAIProvider struct {
	name         string
	providerType string
	baseURL      string
	keyPool      *KeyPool
	defaultModel string
	models       []string
	client       *http.Client
}

// NewOpenAIProvider creates an OpenAI, 9Router, or compatible provider instance
func NewOpenAIProvider(name, providerType, baseURL, apiKey, defaultModel string) *OpenAIProvider {
	var keys []string
	if apiKey != "" {
		keys = []string{apiKey}
	}
	return NewOpenAIProviderWithKeys(name, providerType, baseURL, keys, "round-robin", defaultModel, nil)
}

// NewOpenAIProviderWithKeys creates a provider with multiple keys and model definitions
func NewOpenAIProviderWithKeys(name, providerType, baseURL string, keys []string, keyStrategy string, defaultModel string, models []string) *OpenAIProvider {
	if baseURL == "" {
		switch providerType {
		case "9router":
			baseURL = "https://api.9router.com/v1"
		case "groq":
			baseURL = "https://api.groq.com/openai/v1"
		case "deepseek":
			baseURL = "https://api.deepseek.com/v1"
		case "ollama":
			baseURL = "http://localhost:11434/v1"
		default:
			baseURL = "https://api.openai.com/v1"
		}
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/chat/completions")
	baseURL = strings.TrimSuffix(baseURL, "/chat")
	baseURL = strings.TrimSuffix(baseURL, "/completions")

	if defaultModel == "" {
		switch providerType {
		case "9router":
			defaultModel = "gpt-4o-mini"
		case "groq":
			defaultModel = "llama-3.3-70b-versatile"
		case "deepseek":
			defaultModel = "deepseek-chat"
		case "ollama":
			defaultModel = "llama3"
		default:
			defaultModel = "gpt-4o-mini"
		}
	}

	if len(models) == 0 {
		models = []string{defaultModel}
	}

	return &OpenAIProvider{
		name:         name,
		providerType: providerType,
		baseURL:      baseURL,
		keyPool:      NewKeyPool(keys, keyStrategy),
		defaultModel: defaultModel,
		models:       models,
		client:       &http.Client{Timeout: 90 * time.Second},
	}
}

func (p *OpenAIProvider) Name() string         { return p.name }
func (p *OpenAIProvider) Type() string         { return p.providerType }
func (p *OpenAIProvider) DefaultModel() string { return p.defaultModel }
func (p *OpenAIProvider) Models() []string     { return p.models }
func (p *OpenAIProvider) KeyPool() *KeyPool    { return p.keyPool }

func (p *OpenAIProvider) SetHTTPClient(client interface{}) {
	if c, ok := client.(*http.Client); ok && c != nil {
		p.client = c
	}
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIFunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  tools.ParametersSchema `json:"parameters"`
}

type openAIToolDef struct {
	Type     string            `json:"type"`
	Function openAIFunctionDef `json:"function"`
}

type openAIReqBody struct {
	Model         string          `json:"model"`
	Messages      []openAIMessage `json:"messages"`
	Tools         []openAIToolDef `json:"tools,omitempty"`
	Temperature   float64         `json:"temperature,omitempty"`
	MaxTokens     int             `json:"max_tokens,omitempty"`
	Stream        bool            `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
}

type openAIRespBody struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role             string           `json:"role"`
			Content          string           `json:"content"`
			ReasoningContent string           `json:"reasoning_content,omitempty"`
			ToolCalls        []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		Delta struct {
			Role             string           `json:"role"`
			Content          string           `json:"content"`
			ReasoningContent string           `json:"reasoning_content,omitempty"`
			ToolCalls        []openAIToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		TotalTokens           int `json:"total_tokens"`
		ReasoningTokens       int `json:"reasoning_tokens,omitempty"`
		CompletionTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details,omitempty"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (p *OpenAIProvider) GenerateChat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	start := time.Now()
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

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

	reqPayload := openAIReqBody{
		Model:       model,
		Messages:    msgs,
		Tools:       toolDefs,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream && req.StreamCallback != nil,
	}

	// Enable stream_options for usage in stream mode
	if reqPayload.Stream {
		reqPayload.StreamOptions = &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true}
	}

	payloadBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/chat/completions", p.baseURL)

	keyCount := p.keyPool.Count()
	if keyCount == 0 {
		keyCount = 1
	}

	var lastErr error
	var bodyBytes []byte

	for attempt := 0; attempt < keyCount; attempt++ {
		apiKey := p.keyPool.GetNextKey()

		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(payloadBytes))
		if err != nil {
			return nil, err
		}

		httpReq.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}

		httpResp, err := p.client.Do(httpReq)
		if err != nil {
			if apiKey != "" {
				p.keyPool.MarkError(apiKey, false)
			}
			lastErr = fmt.Errorf("http call failed: %w", err)
			continue
		}

		bodyBytes, err = io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read response: %w", err)
			continue
		}

		if httpResp.StatusCode == 429 || httpResp.StatusCode == 401 || httpResp.StatusCode == 403 {
			if apiKey != "" {
				p.keyPool.MarkError(apiKey, httpResp.StatusCode == 429)
			}
			lastErr = fmt.Errorf("api error (%d): %s", httpResp.StatusCode, string(bodyBytes))
			if attempt < keyCount-1 {
				continue // retry with next key
			}
			return nil, lastErr
		}

		if httpResp.StatusCode >= 400 {
			return nil, fmt.Errorf("api error (%d): %s", httpResp.StatusCode, string(bodyBytes))
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

	// 1. Try standard JSON Unmarshal
	var respBody openAIRespBody
	if err := json.Unmarshal(bodyBytes, &respBody); err == nil {
		if respBody.Error != nil {
			return nil, fmt.Errorf("api error message: %s", respBody.Error.Message)
		}

		if len(respBody.Choices) > 0 {
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

			// Extract thinking/reasoning tokens
			thinkingTokens := respBody.Usage.ReasoningTokens
			if thinkingTokens == 0 && respBody.Usage.CompletionTokensDetails != nil {
				thinkingTokens = respBody.Usage.CompletionTokensDetails.ReasoningTokens
			}

			cost := (float64(respBody.Usage.PromptTokens)*0.00015 + float64(respBody.Usage.CompletionTokens)*0.0006) / 1000.0
			return &ChatResponse{
				Content:          choice.Message.Content,
				Thinking:         choice.Message.ReasoningContent,
				ToolCalls:        parsedToolCalls,
				PromptTokens:     respBody.Usage.PromptTokens,
				CompletionTokens: respBody.Usage.CompletionTokens,
				ThinkingTokens:   thinkingTokens,
				TotalTokens:      respBody.Usage.TotalTokens,
				CostUSD:          cost,
				Latency:          time.Since(start),
				Model:            actualModel,
				ProviderName:     p.name,
			}, nil
		}
	}

	// 2. Fallback: Parse Server-Sent Events (SSE) stream if response starts with "data:"
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
			if totalTokens == 0 {
				totalTokens = combinedContent.Len() / 4
			}
			return &ChatResponse{
				Content:      combinedContent.String(),
				TotalTokens:  totalTokens,
				Latency:      time.Since(start),
				Model:        actualModel,
				ProviderName: p.name,
			}, nil
		}
	}

	// If neither worked, return informative error with snippet of response
	preview := bodyStr
	if len(preview) > 300 {
		preview = preview[:300] + "..."
	}
	return nil, fmt.Errorf("gagal parsing response API (Response: %s)", preview)
}

// GenerateChatStream implements StreamingProvider with real-time SSE streaming
func (p *OpenAIProvider) GenerateChatStream(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	start := time.Now()
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

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

	reqPayload := openAIReqBody{
		Model:       model,
		Messages:    msgs,
		Tools:       toolDefs,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
		StreamOptions: &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true},
	}

	payloadBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal stream request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/chat/completions", p.baseURL)
	apiKey := p.keyPool.GetNextKey()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("stream request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("stream api error (%d): %s", httpResp.StatusCode, string(bodyBytes))
	}

	// Parse SSE stream
	var contentBuilder strings.Builder
	var thinkingBuilder strings.Builder
	var toolCalls []ToolCall
	var promptTokens, completionTokens, totalTokens, thinkingTokens int
	actualModel := model

	// Read line by line from SSE stream
	buf := make([]byte, 4096)
	var lineBuffer strings.Builder

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		n, readErr := httpResp.Body.Read(buf)
		if n > 0 {
			lineBuffer.Write(buf[:n])
			// Process complete lines
			text := lineBuffer.String()
			lines := strings.Split(text, "\n")

			// Keep incomplete last line in buffer
			if !strings.HasSuffix(text, "\n") && len(lines) > 0 {
				lineBuffer.Reset()
				lineBuffer.WriteString(lines[len(lines)-1])
				lines = lines[:len(lines)-1]
			} else {
				lineBuffer.Reset()
			}

			for _, line := range lines {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "data:") {
					continue
				}
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if data == "[DONE]" || data == "" {
					continue
				}

				var chunk openAIRespBody
				if err := json.Unmarshal([]byte(data), &chunk); err != nil {
					continue
				}

				if chunk.Model != "" {
					actualModel = chunk.Model
				}

				if len(chunk.Choices) > 0 {
					delta := chunk.Choices[0].Delta

					if delta.Content != "" {
						contentBuilder.WriteString(delta.Content)
						if req.StreamCallback != nil {
							req.StreamCallback(StreamChunk{Content: delta.Content})
						}
					}

					if delta.ReasoningContent != "" {
						thinkingBuilder.WriteString(delta.ReasoningContent)
						if req.StreamCallback != nil {
							req.StreamCallback(StreamChunk{Thinking: delta.ReasoningContent})
						}
					}

					// Accumulate tool calls from stream
					for _, tc := range delta.ToolCalls {
						if tc.ID != "" {
							var args map[string]interface{}
							_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
							toolCalls = append(toolCalls, ToolCall{
								ID:        tc.ID,
								Name:      tc.Function.Name,
								Arguments: args,
							})
						}
					}
				}

				// Capture usage from final chunk
				if chunk.Usage.TotalTokens > 0 {
					promptTokens = chunk.Usage.PromptTokens
					completionTokens = chunk.Usage.CompletionTokens
					totalTokens = chunk.Usage.TotalTokens
					thinkingTokens = chunk.Usage.ReasoningTokens
					if thinkingTokens == 0 && chunk.Usage.CompletionTokensDetails != nil {
						thinkingTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
					}
				}
			}
		}

		if readErr != nil {
			break
		}
	}

	// Send done signal
	if req.StreamCallback != nil {
		req.StreamCallback(StreamChunk{Done: true})
	}

	if apiKey != "" {
		p.keyPool.MarkSuccess(apiKey)
	}

	if totalTokens == 0 {
		totalTokens = (contentBuilder.Len() + thinkingBuilder.Len()) / 4
	}

	cost := (float64(promptTokens)*0.00015 + float64(completionTokens)*0.0006) / 1000.0

	return &ChatResponse{
		Content:          contentBuilder.String(),
		Thinking:         thinkingBuilder.String(),
		ToolCalls:        toolCalls,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		ThinkingTokens:   thinkingTokens,
		TotalTokens:      totalTokens,
		CostUSD:          cost,
		Latency:          time.Since(start),
		Model:            actualModel,
		ProviderName:     p.name,
	}, nil
}

// Ensure OpenAIProvider implements StreamingProvider
var _ StreamingProvider = (*OpenAIProvider)(nil)
