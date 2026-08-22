package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// FetchRemoteModels queries the provider's /models endpoint to automatically detect available models
func FetchRemoteModels(ctx context.Context, pType, baseURL, apiKey string, client *http.Client) ([]string, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	pType = strings.ToLower(pType)

	switch pType {
	case "gemini":
		return fetchGeminiModels(ctx, apiKey, client)
	case "anthropic":
		return fetchAnthropicModels(ctx, apiKey, client)
	default: // 9router, openai, groq, deepseek, ollama, custom
		return fetchOpenAICompatibleModels(ctx, pType, baseURL, apiKey, client)
	}
}

func fetchOpenAICompatibleModels(ctx context.Context, pType, baseURL, apiKey string, client *http.Client) ([]string, error) {
	if baseURL == "" {
		switch pType {
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

	// 1. Try standard OpenAI /models endpoint
	endpoint := fmt.Sprintf("%s/models", baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi endpoint models (%s): %w", endpoint, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		trimmedBody := strings.TrimSpace(string(bodyBytes))
		if len(trimmedBody) > 100 {
			trimmedBody = trimmedBody[:100] + "..."
		}
		return nil, fmt.Errorf("api error (%d): %s", resp.StatusCode, trimmedBody)
	}

	// Parsing OpenAI response: {"data": [{"id": "gpt-4o"}, ...]}
	var openAIResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		// Ollama format: {"models": [{"name": "llama3"}, ...]}
		Models []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"models"`
	}

	if err := json.Unmarshal(bodyBytes, &openAIResp); err != nil {
		trimmedBody := strings.TrimSpace(string(bodyBytes))
		if len(trimmedBody) > 80 {
			trimmedBody = trimmedBody[:80] + "..."
		}
		return nil, fmt.Errorf("endpoint /models tidak mengembalikan JSON valid (respon: %s)", trimmedBody)
	}

	var models []string
	for _, m := range openAIResp.Data {
		id := strings.TrimSpace(m.ID)
		if id != "" && !isExcludedModel(id) {
			models = append(models, id)
		}
	}

	for _, m := range openAIResp.Models {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = strings.TrimSpace(m.ID)
		}
		if name != "" && !isExcludedModel(name) {
			models = append(models, name)
		}
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("tidak ada model chat yang ditemukan dari /models")
	}

	sort.Strings(models)
	return deduplicate(models), nil
}

func fetchGeminiModels(ctx context.Context, apiKey string, client *http.Client) ([]string, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("api key Gemini diperlukan untuk mendeteksi model")
	}

	endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s", apiKey)
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi Google Gemini API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("gemini api error (%d): %s", resp.StatusCode, string(bodyBytes))
	}

	var geminiResp struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}

	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		return nil, err
	}

	var models []string
	for _, m := range geminiResp.Models {
		// Ensure model supports generateContent
		hasGenerate := false
		for _, method := range m.SupportedGenerationMethods {
			if method == "generateContent" {
				hasGenerate = true
				break
			}
		}
		if hasGenerate {
			modelID := strings.TrimPrefix(m.Name, "models/")
			models = append(models, modelID)
		}
	}

	if len(models) == 0 {
		models = []string{"gemini-2.0-flash", "gemini-1.5-pro", "gemini-1.5-flash"}
	}

	sort.Strings(models)
	return deduplicate(models), nil
}

func fetchAnthropicModels(ctx context.Context, apiKey string, client *http.Client) ([]string, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("api key Anthropic diperlukan")
	}

	endpoint := "https://api.anthropic.com/v1/models"
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 200 {
			var antResp struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal(bodyBytes, &antResp); err == nil && len(antResp.Data) > 0 {
				var models []string
				for _, m := range antResp.Data {
					models = append(models, m.ID)
				}
				sort.Strings(models)
				return deduplicate(models), nil
			}
		}
	}

	// Standard Anthropic Models list
	return []string{
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
		"claude-3-opus-20240229",
		"claude-3-sonnet-20240229",
		"claude-3-haiku-20240307",
	}, nil
}

func isExcludedModel(name string) bool {
	lower := strings.ToLower(name)
	// Exclude non-chat embeddings, audio, tts, moderation, whisper, dall-e
	excludes := []string{"embedding", "text-embedding", "whisper", "tts", "dall-e", "moderation", "babbage", "davinci-002", "curie"}
	for _, ex := range excludes {
		if strings.Contains(lower, ex) {
			return true
		}
	}
	return false
}

func deduplicate(slice []string) []string {
	seen := make(map[string]bool)
	var res []string
	for _, s := range slice {
		if !seen[s] && s != "" {
			seen[s] = true
			res = append(res, s)
		}
	}
	return res
}
