package webadmin

import (
	"encoding/json"
	"fmt"
	"strings"
)

// OpenAIModel represents a single model in OpenAI /v1/models response
type OpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// OpenAIModelListResponse is the response structure for GET /v1/models
type OpenAIModelListResponse struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
}

// OpenAIChatMessage represents a message in /v1/chat/completions
type OpenAIChatMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content"` // can be string or []interface{} (multimodal)
	Name       string      `json:"name,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
}

// GetContentString parses the content field which may be a string or a structured multimodal array
func (m *OpenAIChatMessage) GetContentString() (string, []string) {
	if m.Content == nil {
		return "", nil
	}

	switch v := m.Content.(type) {
	case string:
		return v, nil
	case []interface{}:
		var textParts []string
		var images []string
		for _, part := range v {
			if partMap, ok := part.(map[string]interface{}); ok {
				pType, _ := partMap["type"].(string)
				switch pType {
				case "text":
					if txt, ok := partMap["text"].(string); ok {
						textParts = append(textParts, txt)
					}
				case "image_url":
					if imgObj, ok := partMap["image_url"].(map[string]interface{}); ok {
						if url, ok := imgObj["url"].(string); ok {
							images = append(images, url)
						}
					}
				}
			}
		}
		return strings.Join(textParts, "\n"), images
	default:
		// Fallback JSON stringification
		b, err := json.Marshal(v)
		if err == nil {
			return string(b), nil
		}
		return fmt.Sprintf("%v", v), nil
	}
}

// OpenAIChatRequest represents the request body for POST /v1/chat/completions
type OpenAIChatRequest struct {
	Model           string              `json:"model"`
	Messages        []OpenAIChatMessage `json:"messages"`
	Stream          bool                `json:"stream,omitempty"`
	Temperature     *float64            `json:"temperature,omitempty"`
	MaxTokens       *int                `json:"max_tokens,omitempty"`
	ReasoningEffort string              `json:"reasoning_effort,omitempty"` // "low", "medium", "high"
	ThinkingLevel   string              `json:"thinking_level,omitempty"`  // alternative naming
	ThinkingBudget  *int                `json:"thinking_budget,omitempty"`
	User            string              `json:"user,omitempty"`
}

// OpenAIUsage tracks token usage
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIChoiceMessage represents the response message
type OpenAIChoiceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIChoice represents a single completion choice
type OpenAIChoice struct {
	Index        int                 `json:"index"`
	Message      OpenAIChoiceMessage `json:"message"`
	FinishReason string              `json:"finish_reason"`
}

// OpenAIChatResponse is the non-streaming response body for POST /v1/chat/completions
type OpenAIChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   OpenAIUsage    `json:"usage"`
}

// OpenAIChatStreamDelta represents delta content in streaming
type OpenAIChatStreamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// OpenAIChatStreamChoice represents a streaming choice
type OpenAIChatStreamChoice struct {
	Index        int                   `json:"index"`
	Delta        OpenAIChatStreamDelta `json:"delta"`
	FinishReason *string               `json:"finish_reason"`
}

// OpenAIChatStreamChunk is a single SSE data payload
type OpenAIChatStreamChunk struct {
	ID      string                   `json:"id"`
	Object  string                   `json:"object"`
	Created int64                    `json:"created"`
	Model   string                   `json:"model"`
	Choices []OpenAIChatStreamChoice `json:"choices"`
}
