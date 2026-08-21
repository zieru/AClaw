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
		client:       &http.Client{Timeout: 90 * time.Second},
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
	if keyCount == 0 {
		keyCount = 1
	}

	var lastErr error
	var respBytes []byte

	for attempt := 0; attempt < keyCount; attempt++ {
		apiKey := p.keyPool.GetNextKey()
		url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
		httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payloadBytes))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")

		httpResp, err := p.client.Do(httpReq)
		if err != nil {
			if apiKey != "" {
				p.keyPool.MarkError(apiKey, false)
			}
			lastErr = fmt.Errorf("gemini api call failed: %w", err)
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
			lastErr = fmt.Errorf("gemini api error (%d): %s", httpResp.StatusCode, string(respBytes))
			if attempt < keyCount-1 {
				continue // retry with next key
			}
			return nil, lastErr
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
