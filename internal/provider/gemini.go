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

	"goassistant/internal/config"
	"goassistant/internal/tools"
)

// GeminiProvider handles Google Gemini API
type GeminiProvider struct {
	name         string
	keyPool      *KeyPool
	defaultModel string
	models       []string
	client       *http.Client
}

func NewGeminiProvider(name, apiKey, defaultModel string) *GeminiProvider {
	var keys []string
	if apiKey != "" {
		keys = []string{apiKey}
	}
	return NewGeminiProviderWithKeys(name, keys, "round-robin", defaultModel, nil)
}

func NewGeminiProviderWithKeys(name string, keys []string, keyStrategy string, defaultModel string, models []string) *GeminiProvider {
	if defaultModel == "" {
		defaultModel = "gemini-2.0-flash"
	}
	if len(models) == 0 {
		models = []string{"gemini-2.0-flash", "gemini-1.5-pro", "gemini-1.5-flash"}
	}
	return &GeminiProvider{
		name:         name,
		keyPool:      NewKeyPool(keys, keyStrategy),
		defaultModel: defaultModel,
		models:       models,
		client:       &http.Client{Timeout: time.Duration(config.Get().Timeouts.APICallSeconds) * time.Second},
	}
}

func (p *GeminiProvider) Name() string         { return p.name }
func (p *GeminiProvider) Type() string         { return "gemini" }
func (p *GeminiProvider) DefaultModel() string { return p.defaultModel }
func (p *GeminiProvider) Models() []string     { return p.models }
func (p *GeminiProvider) KeyPool() *KeyPool    { return p.keyPool }

func (p *GeminiProvider) SetHTTPClient(client interface{}) {
	if c, ok := client.(*http.Client); ok && c != nil {
		p.client = c
	}
}

type geminiPart struct {
	Text             string                 `json:"text,omitempty"`
	Thought          string                 `json:"thought,omitempty"`
	FunctionCall     *geminiFunctionCall    `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResp    `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type geminiFunctionResp struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  tools.ParametersSchema `json:"parameters"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiReqBody struct {
	Contents          []geminiContent `json:"contents"`
	Tools             []geminiTool    `json:"tools,omitempty"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenConfig `json:"generationConfig,omitempty"`
}

type geminiGenConfig struct {
	Temperature    float64             `json:"temperature,omitempty"`
	MaxTokens      int                 `json:"maxOutputTokens,omitempty"`
	ThinkingConfig *geminiThinkConfig  `json:"thinkingConfig,omitempty"`
}

type geminiThinkConfig struct {
	ThinkingBudget int  `json:"thinkingBudget,omitempty"`
	IncludeThoughts bool `json:"includeThoughts,omitempty"`
}

type geminiRespBody struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
			Role  string       `json:"role"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
		ThoughtsTokenCount   int `json:"thoughtsTokenCount,omitempty"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func (p *GeminiProvider) GenerateChat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	start := time.Now()
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	var contents []geminiContent
	var systemContent *geminiContent

	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			systemContent = &geminiContent{
				Parts: []geminiPart{{Text: m.Content}},
			}
			continue
		}

		role := "user"
		if m.Role == RoleAssistant {
			role = "model"
		}

		var parts []geminiPart
		if m.Content != "" {
			parts = append(parts, geminiPart{Text: m.Content})
		}

		for _, tc := range m.ToolCalls {
			parts = append(parts, geminiPart{
				FunctionCall: &geminiFunctionCall{
					Name: tc.Name,
					Args: tc.Arguments,
				},
			})
		}

		if m.Role == RoleTool {
			role = "function"
			parts = append(parts, geminiPart{
				FunctionResponse: &geminiFunctionResp{
					Name: m.Name,
					Response: map[string]interface{}{
						"output": m.Content,
					},
				},
			})
		}

		if len(parts) > 0 {
			contents = append(contents, geminiContent{
				Role:  role,
				Parts: parts,
			})
		}
	}

	var toolsList []geminiTool
	if len(req.Tools) > 0 {
		var decls []geminiFunctionDeclaration
		for _, t := range req.Tools {
			decls = append(decls, geminiFunctionDeclaration{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			})
		}
		toolsList = append(toolsList, geminiTool{FunctionDeclarations: decls})
	}

	payload := geminiReqBody{
		Contents:          contents,
		Tools:             toolsList,
		SystemInstruction: systemContent,
	}

	if req.Temperature > 0 || req.MaxTokens > 0 || req.ThinkingEnabled {
		payload.GenerationConfig = &geminiGenConfig{
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
		}
		if req.ThinkingEnabled {
			budget := req.ThinkingBudget
			if budget <= 0 {
				budget = 8192
			}
			payload.GenerationConfig.ThinkingConfig = &geminiThinkConfig{
				ThinkingBudget:  budget,
				IncludeThoughts: true,
			}
		}
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal gemini payload: %w", err)
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
		url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)

		// Per-attempt timeout to prevent single key from hanging the entire request
		attemptTimeout := 35 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining > 0 && remaining < attemptTimeout {
				attemptTimeout = remaining
			}
		}
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, attemptTimeout)

		httpReq, err := http.NewRequestWithContext(attemptCtx, "POST", url, bytes.NewBuffer(payloadBytes))
		if err != nil {
			cancelAttempt()
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")

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
			lastErr = fmt.Errorf("gemini api call failed: %w", err)
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
			lastErr = fmt.Errorf("gemini rate limit error (429): %s", string(respBytes))
			continue // retry with next key
		}

		if httpResp.StatusCode == 401 || httpResp.StatusCode == 403 {
			if apiKey != "" {
				p.keyPool.MarkAuthError(apiKey)
			}
			lastErr = fmt.Errorf("gemini auth error (%d): %s", httpResp.StatusCode, string(respBytes))
			continue // retry with next key
		}

		if httpResp.StatusCode >= 500 {
			if apiKey != "" {
				p.keyPool.MarkTimeout(apiKey)
			}
			lastErr = fmt.Errorf("gemini server error (%d): %s", httpResp.StatusCode, string(respBytes))
			continue // retry with next key
		}

		if httpResp.StatusCode >= 400 {
			return nil, fmt.Errorf("gemini api error (%d): %s", httpResp.StatusCode, string(respBytes))
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

	var respBody geminiRespBody
	if err := json.Unmarshal(respBytes, &respBody); err != nil {
		return nil, fmt.Errorf("failed to unmarshal gemini response: %w", err)
	}

	if respBody.Error != nil {
		return nil, fmt.Errorf("gemini error: %s", respBody.Error.Message)
	}

	if len(respBody.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates returned from gemini")
	}

	cand := respBody.Candidates[0]
	var textParts []string
	var thinkingParts []string
	var toolCalls []ToolCall

	for i, part := range cand.Content.Parts {
		if part.Thought != "" {
			thinkingParts = append(thinkingParts, part.Thought)
		} else if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			toolCalls = append(toolCalls, ToolCall{
				ID:        fmt.Sprintf("call_%d_%d", time.Now().Unix(), i),
				Name:      part.FunctionCall.Name,
				Arguments: part.FunctionCall.Args,
			})
		}
	}

	var promptTokens, compTokens, totalTokens, thinkingTokens int
	if respBody.UsageMetadata != nil {
		promptTokens = respBody.UsageMetadata.PromptTokenCount
		compTokens = respBody.UsageMetadata.CandidatesTokenCount
		totalTokens = respBody.UsageMetadata.TotalTokenCount
		thinkingTokens = respBody.UsageMetadata.ThoughtsTokenCount
	}

	// Cost estimation for Gemini 2.0 Flash (~$0.10/1M input, $0.40/1M output)
	cost := (float64(promptTokens)*0.00010 + float64(compTokens)*0.00040) / 1000.0

	return &ChatResponse{
		Content:          strings.Join(textParts, "\n"),
		Thinking:         strings.Join(thinkingParts, "\n"),
		ToolCalls:        toolCalls,
		PromptTokens:     promptTokens,
		CompletionTokens: compTokens,
		ThinkingTokens:   thinkingTokens,
		TotalTokens:      totalTokens,
		CostUSD:          cost,
		Latency:          time.Since(start),
		Model:            model,
		ProviderName:     p.name,
	}, nil
}

// GenerateChatStream implements StreamingProvider for Gemini using streamGenerateContent?alt=sse
func (p *GeminiProvider) GenerateChatStream(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	start := time.Now()
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	var contents []geminiContent
	var systemContent *geminiContent

	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			systemContent = &geminiContent{Parts: []geminiPart{{Text: m.Content}}}
			continue
		}
		role := "user"
		if m.Role == RoleAssistant {
			role = "model"
		}
		var parts []geminiPart
		if m.Content != "" {
			parts = append(parts, geminiPart{Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			parts = append(parts, geminiPart{FunctionCall: &geminiFunctionCall{Name: tc.Name, Args: tc.Arguments}})
		}
		if m.Role == RoleTool {
			role = "function"
			parts = append(parts, geminiPart{FunctionResponse: &geminiFunctionResp{
				Name: m.Name, Response: map[string]interface{}{"output": m.Content},
			}})
		}
		if len(parts) > 0 {
			contents = append(contents, geminiContent{Role: role, Parts: parts})
		}
	}

	var toolsList []geminiTool
	if len(req.Tools) > 0 {
		var decls []geminiFunctionDeclaration
		for _, t := range req.Tools {
			decls = append(decls, geminiFunctionDeclaration{
				Name: t.Name(), Description: t.Description(), Parameters: t.Parameters(),
			})
		}
		toolsList = append(toolsList, geminiTool{FunctionDeclarations: decls})
	}

	payload := geminiReqBody{Contents: contents, Tools: toolsList, SystemInstruction: systemContent}
	if req.Temperature > 0 || req.MaxTokens > 0 || req.ThinkingEnabled {
		payload.GenerationConfig = &geminiGenConfig{Temperature: req.Temperature, MaxTokens: req.MaxTokens}
		if req.ThinkingEnabled {
			budget := req.ThinkingBudget
			if budget <= 0 {
				budget = 8192
			}
			payload.GenerationConfig.ThinkingConfig = &geminiThinkConfig{ThinkingBudget: budget, IncludeThoughts: true}
		}
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
		url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s", model, apiKey)
		httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payloadBytes))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err = p.client.Do(httpReq)
		if err != nil {
			if apiKey != "" {
				p.keyPool.MarkTimeout(apiKey)
			}
			lastErr = fmt.Errorf("gemini stream request failed: %w", err)
			continue
		}

		if resp.StatusCode == 429 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if apiKey != "" {
				p.keyPool.MarkRateLimit(apiKey)
			}
			lastErr = fmt.Errorf("gemini stream rate limit error (429): %s", string(bodyBytes))
			continue
		}

		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if apiKey != "" {
				p.keyPool.MarkAuthError(apiKey)
			}
			lastErr = fmt.Errorf("gemini stream auth error (%d): %s", resp.StatusCode, string(bodyBytes))
			continue
		}

		if resp.StatusCode >= 500 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if apiKey != "" {
				p.keyPool.MarkTimeout(apiKey)
			}
			lastErr = fmt.Errorf("gemini stream server error (%d): %s", resp.StatusCode, string(bodyBytes))
			continue
		}

		if resp.StatusCode >= 400 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("gemini stream api error (%d): %s", resp.StatusCode, string(bodyBytes))
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
	var promptTokens, compTokens, totalTokens, thinkingTokens int
	actualModel := model

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
				if !strings.HasPrefix(line, "data:") {
					continue
				}
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if data == "" {
					continue
				}

				var streamResp struct {
					Candidates []struct {
						Content struct {
							Parts    []geminiPart `json:"parts"`
							Role     string       `json:"role"`
						} `json:"content"`
						FinishReason string `json:"finishReason"`
					} `json:"candidates"`
					UsageMetadata *struct {
						PromptTokenCount     int `json:"promptTokenCount"`
						CandidatesTokenCount int `json:"candidatesTokenCount"`
						TotalTokenCount      int `json:"totalTokenCount"`
						ThoughtsTokenCount   int `json:"thoughtsTokenCount,omitempty"`
					} `json:"usageMetadata"`
				}
				if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
					continue
				}

				if len(streamResp.Candidates) > 0 {
					cand := streamResp.Candidates[0]
					for _, part := range cand.Content.Parts {
						if part.Thought != "" {
							thinkingBuilder.WriteString(part.Thought)
							if req.StreamCallback != nil {
								req.StreamCallback(StreamChunk{Thinking: part.Thought})
							}
						} else if part.Text != "" {
							contentBuilder.WriteString(part.Text)
							if req.StreamCallback != nil {
								req.StreamCallback(StreamChunk{Content: part.Text})
							}
						}
						if part.FunctionCall != nil {
							toolCalls = append(toolCalls, ToolCall{
								ID:   fmt.Sprintf("call_%d_%d", time.Now().Unix(), len(toolCalls)),
								Name: part.FunctionCall.Name,
								Arguments: part.FunctionCall.Args,
							})
						}
					}
					if cand.FinishReason != "" {
						if req.StreamCallback != nil {
							req.StreamCallback(StreamChunk{Done: true})
						}
					}
				}

				if streamResp.UsageMetadata != nil {
					promptTokens = streamResp.UsageMetadata.PromptTokenCount
					compTokens = streamResp.UsageMetadata.CandidatesTokenCount
					totalTokens = streamResp.UsageMetadata.TotalTokenCount
					thinkingTokens = streamResp.UsageMetadata.ThoughtsTokenCount
				}
			}
		}
		if readErr != nil {
			break
		}
	}

	cost := (float64(promptTokens)*0.00010 + float64(compTokens)*0.00040) / 1000.0
	if totalTokens == 0 {
		totalTokens = (contentBuilder.Len() + thinkingBuilder.Len()) / 4
	}

	return &ChatResponse{
		Content:          contentBuilder.String(),
		Thinking:         thinkingBuilder.String(),
		ToolCalls:        toolCalls,
		PromptTokens:     promptTokens,
		CompletionTokens: compTokens,
		ThinkingTokens:   thinkingTokens,
		TotalTokens:      totalTokens,
		CostUSD:          cost,
		Latency:          time.Since(start),
		Model:            actualModel,
		ProviderName:     p.name,
	}, nil
}

// Ensure GeminiProvider implements StreamingProvider
var _ StreamingProvider = (*GeminiProvider)(nil)
