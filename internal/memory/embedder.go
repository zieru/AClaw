package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/storage"
)

// EmbedTestResult contains diagnostic details from a live connection test
type EmbedTestResult struct {
	Success      bool          `json:"success"`
	StatusCode   int           `json:"status_code"`
	Provider     string        `json:"provider"`
	Model        string        `json:"model"`
	Endpoint     string        `json:"endpoint"`
	KeySource    string        `json:"key_source"`
	Latency      time.Duration `json:"latency"`
	Dimensions   int           `json:"dimensions"`
	SampleVector []float32     `json:"sample_vector,omitempty"`
	Message      string        `json:"message,omitempty"`
	ErrorMessage string        `json:"error_message,omitempty"`
}

// Embedder generates vector embeddings for text
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	IsEnabled() bool
	Dimensions() int
	TestConnection(ctx context.Context) (*EmbedTestResult, error)
	ResolveAPIKey() (string, string)
}

// ClientEmbedder implements Embedder supporting Google Gemini, OpenAI, Ollama, and Custom endpoints
type ClientEmbedder struct {
	cfg        config.EmbeddingConfig
	db         *storage.DB
	httpClient *http.Client
}

// NewEmbedder creates a new Embedder instance based on config and optional storage DB
func NewEmbedder(cfg config.EmbeddingConfig, db *storage.DB) Embedder {
	return &ClientEmbedder{
		cfg: cfg,
		db:  db,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (e *ClientEmbedder) IsEnabled() bool {
	return e != nil && e.cfg.Enabled && e.cfg.Model != ""
}

func (e *ClientEmbedder) Dimensions() int {
	if e == nil || e.cfg.Dimensions <= 0 {
		if strings.EqualFold(e.cfg.Provider, "gemini") {
			return 768
		}
		return 1536
	}
	return e.cfg.Dimensions
}

func isCookieOrInvalidKey(k string) bool {
	k = strings.TrimSpace(k)
	if k == "" {
		return true
	}
	if strings.HasPrefix(k, "__Secure-") || strings.HasPrefix(k, "SAPISID") || strings.HasPrefix(k, "SSID") {
		return true
	}
	if strings.Contains(k, ";") || strings.Contains(k, "=") {
		return true
	}
	return false
}

func truncateKey(k string) string {
	if len(k) <= 8 {
		return k
	}
	return k[:4] + "..." + k[len(k)-4:]
}

// ResolveAPIKey determines the API key to use and describes its source
func (e *ClientEmbedder) ResolveAPIKey() (string, string) {
	if e == nil {
		return "", "None"
	}
	// 1. Explicitly configured API Key in memory settings
	if strings.TrimSpace(e.cfg.APIKey) != "" {
		manualKey := strings.TrimSpace(e.cfg.APIKey)
		if isCookieOrInvalidKey(manualKey) {
			return manualKey, "Kunci Manual (Peringatan: Berupa cookie web, bukan API key resmi)"
		}
		return manualKey, "Pengaturan Memory Manual"
	}

	targetProv := strings.ToLower(strings.TrimSpace(e.cfg.Provider))
	if targetProv == "" {
		targetProv = "openai"
	}

	hasGeminiWebOnly := false

	// 2. Query GoAssistant database for an active provider with matching type or name
	if e.db != nil {
		if providers, err := e.db.ListProviders(); err == nil {
			for _, p := range providers {
				pType := strings.ToLower(strings.TrimSpace(p.Type))
				pName := strings.ToLower(strings.TrimSpace(p.Name))

				// Never treat web scrapers as REST embedding providers
				if pType == "gemini_web" || strings.Contains(pType, "web") || strings.Contains(pType, "scrape") {
					if targetProv == "gemini" && p.IsActive {
						hasGeminiWebOnly = true
					}
					continue
				}

				isMatch := false
				if targetProv == "gemini" {
					if pType == "gemini" {
						isMatch = true
					} else if strings.Contains(pName, "gemini") && !strings.Contains(pName, "web") && !strings.Contains(pName, "scrape") {
						isMatch = true
					}
				} else if targetProv == "openai" {
					if pType == "openai" || pType == "9router" || pType == "custom" {
						isMatch = true
					} else if strings.Contains(pName, "openai") {
						isMatch = true
					}
				} else {
					if pType == targetProv || pName == targetProv {
						isMatch = true
					}
				}

				if isMatch && p.IsActive {
					key := ""
					if len(p.APIKeys) > 0 && strings.TrimSpace(p.APIKeys[0]) != "" {
						key = strings.TrimSpace(p.APIKeys[0])
					} else if strings.TrimSpace(p.APIKey) != "" {
						key = strings.TrimSpace(p.APIKey)
					}
					if key != "" && !isCookieOrInvalidKey(key) {
						return key, fmt.Sprintf("Provider Database '%s'", p.Name)
					}
				}
			}
		}
	}

	// 3. Fallback to Environment Variables
	switch targetProv {
	case "gemini":
		if envKey := os.Getenv("GEMINI_API_KEY"); envKey != "" && !isCookieOrInvalidKey(envKey) {
			return envKey, "Environment (GEMINI_API_KEY)"
		}
	case "openai":
		if envKey := os.Getenv("OPENAI_API_KEY"); envKey != "" && !isCookieOrInvalidKey(envKey) {
			return envKey, "Environment (OPENAI_API_KEY)"
		}
	}

	if targetProv == "gemini" && hasGeminiWebOnly {
		return "", "Provider 'Gemini Web' adalah web scraper cookie dan tidak mendukung REST Embedding API. Harap setel kunci resmi Google AI Studio via /memory key <api_key> (diawali AIzaSy...)"
	}

	return "", "Tidak Ditemukan"
}

// Embed generates an embedding vector for a single text string
func (e *ClientEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if !e.IsEnabled() || strings.TrimSpace(text) == "" {
		return nil, nil
	}

	results, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("empty embedding returned")
	}
	return results[0], nil
}

// EmbedBatch generates embeddings for multiple text strings
func (e *ClientEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if !e.IsEnabled() || len(texts) == 0 {
		return nil, nil
	}

	prov := strings.ToLower(strings.TrimSpace(e.cfg.Provider))
	if prov == "gemini" {
		return e.embedGemini(ctx, texts)
	}

	return e.embedOpenAICompatible(ctx, texts)
}

// --- Google Gemini Native Implementation ---

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiEmbedContentRequest struct {
	Model   string        `json:"model"`
	Content geminiContent `json:"content"`
}

type geminiBatchEmbedContentsRequest struct {
	Requests []geminiEmbedContentRequest `json:"requests"`
}

type geminiEmbedContentResponse struct {
	Embedding *struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

type geminiBatchEmbedContentsResponse struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

func normalizeGeminiModel(m string) string {
	m = strings.TrimSpace(m)
	if m == "" {
		return "models/text-embedding-004"
	}
	if !strings.HasPrefix(m, "models/") {
		return "models/" + m
	}
	return m
}

func (e *ClientEmbedder) embedGemini(ctx context.Context, texts []string) ([][]float32, error) {
	apiKey, keySource := e.ResolveAPIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("API key Gemini tidak ditemukan (%s). Harap isi via /memory key <api_key> (diawali AIzaSy...) atau daftarkan provider Gemini resmi", keySource)
	}

	if isCookieOrInvalidKey(apiKey) {
		return nil, fmt.Errorf("kunci '%s' terdeteksi sebagai cookie sesi web, bukan Google AI Studio API Key. Harap gunakan kunci resmi (diawali 'AIzaSy...') via /memory key <api_key>", truncateKey(apiKey))
	}

	baseURL := strings.TrimRight(e.cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	modelWithPrefix := normalizeGeminiModel(e.cfg.Model)

	// Single item embedContent
	if len(texts) == 1 {
		reqURL := fmt.Sprintf("%s/v1beta/%s:embedContent?key=%s", baseURL, modelWithPrefix, url.QueryEscape(apiKey))
		reqBody := geminiEmbedContentRequest{
			Model: modelWithPrefix,
			Content: geminiContent{
				Parts: []geminiPart{{Text: texts[0]}},
			},
		}

		data, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("marshal gemini request: %w", err)
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("create gemini request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-goog-api-key", apiKey)

		resp, err := e.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("call gemini embedding api: %w", err)
		}
		defer resp.Body.Close()

		bodyBytes, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			bodyStr := string(bodyBytes)
			if strings.Contains(bodyStr, "<html") || strings.Contains(bodyStr, "<!DOCTYPE") {
				return nil, fmt.Errorf("gemini api error (HTTP %d): Bad Request dari Google Front End (periksa kembali API key Google AI Studio Anda)", resp.StatusCode)
			}
			return nil, fmt.Errorf("gemini embedding error (%d): %s", resp.StatusCode, bodyStr)
		}

		var parsed geminiEmbedContentResponse
		if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
			return nil, fmt.Errorf("unmarshal gemini response: %w", err)
		}

		if parsed.Error != nil && parsed.Error.Message != "" {
			return nil, fmt.Errorf("gemini api error: %s", parsed.Error.Message)
		}
		if parsed.Embedding == nil || len(parsed.Embedding.Values) == 0 {
			return nil, fmt.Errorf("gemini returned empty vector")
		}

		return [][]float32{parsed.Embedding.Values}, nil
	}

	// Batch embedContents
	reqURL := fmt.Sprintf("%s/v1beta/%s:batchEmbedContents?key=%s", baseURL, modelWithPrefix, url.QueryEscape(apiKey))
	var batchReq geminiBatchEmbedContentsRequest
	for _, t := range texts {
		batchReq.Requests = append(batchReq.Requests, geminiEmbedContentRequest{
			Model: modelWithPrefix,
			Content: geminiContent{
				Parts: []geminiPart{{Text: t}},
			},
		})
	}

	data, err := json.Marshal(batchReq)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini batch request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create gemini batch request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", apiKey)

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call gemini batch api: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		bodyStr := string(bodyBytes)
		if strings.Contains(bodyStr, "<html") || strings.Contains(bodyStr, "<!DOCTYPE") {
			return nil, fmt.Errorf("gemini api error (HTTP %d): Bad Request dari Google Front End (periksa kembali API key Google AI Studio Anda)", resp.StatusCode)
		}
		return nil, fmt.Errorf("gemini batch embedding error (%d): %s", resp.StatusCode, bodyStr)
	}

	var parsed geminiBatchEmbedContentsResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal gemini batch response: %w", err)
	}

	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, fmt.Errorf("gemini batch api error: %s", parsed.Error.Message)
	}

	results := make([][]float32, len(parsed.Embeddings))
	for i, it := range parsed.Embeddings {
		results[i] = it.Values
	}
	return results, nil
}

// --- OpenAI / Ollama / Custom Compatible Implementation ---

type openAIEmbeddingRequest struct {
	Model string      `json:"model"`
	Input interface{} `json:"input"` // string or []string
}

type openAIEmbeddingResponseItem struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type openAIEmbeddingResponse struct {
	Data  []openAIEmbeddingResponseItem `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (e *ClientEmbedder) embedOpenAICompatible(ctx context.Context, texts []string) ([][]float32, error) {
	baseURL := strings.TrimRight(e.cfg.BaseURL, "/")
	prov := strings.ToLower(strings.TrimSpace(e.cfg.Provider))

	if baseURL == "" {
		if prov == "ollama" {
			baseURL = "http://localhost:11434/v1"
		} else {
			baseURL = "https://api.openai.com/v1"
		}
	}
	if !strings.HasSuffix(baseURL, "/embeddings") {
		baseURL = baseURL + "/embeddings"
	}

	apiKey, _ := e.ResolveAPIKey()

	reqBody := openAIEmbeddingRequest{
		Model: e.cfg.Model,
		Input: texts,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call embedding api: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding api error (%d): %s", resp.StatusCode, string(bodyBytes))
	}

	var parsed openAIEmbeddingResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal embedding response: %w", err)
	}

	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, fmt.Errorf("embedding error: %s", parsed.Error.Message)
	}

	results := make([][]float32, len(texts))
	for _, item := range parsed.Data {
		if item.Index >= 0 && item.Index < len(results) {
			results[item.Index] = item.Embedding
		}
	}

	return results, nil
}

// TestConnection verifies live connectivity with the configured embedding endpoint
func (e *ClientEmbedder) TestConnection(ctx context.Context) (*EmbedTestResult, error) {
	if e == nil {
		return nil, fmt.Errorf("embedder is nil")
	}
	model := e.cfg.Model
	if model == "" {
		model = "text-embedding-004"
	}
	prov := e.cfg.Provider
	if prov == "" {
		prov = "gemini"
	}

	_, keySource := e.ResolveAPIKey()
	endpointDesc := e.cfg.BaseURL
	if endpointDesc == "" {
		if strings.EqualFold(prov, "gemini") {
			endpointDesc = "https://generativelanguage.googleapis.com/v1beta/" + normalizeGeminiModel(model) + ":embedContent"
		} else if strings.EqualFold(prov, "ollama") {
			endpointDesc = "http://localhost:11434/v1/embeddings"
		} else {
			endpointDesc = "https://api.openai.com/v1/embeddings"
		}
	}

	start := time.Now()
	vec, err := e.Embed(ctx, "GoAssistant Memory Diagnostic Test")
	latency := time.Since(start)

	res := &EmbedTestResult{
		Provider:   prov,
		Model:      model,
		Endpoint:   endpointDesc,
		KeySource:  keySource,
		Latency:    latency,
		StatusCode: 200,
	}

	if err != nil {
		res.Success = false
		res.ErrorMessage = err.Error()
		res.Message = err.Error()
		res.StatusCode = 500
		return res, err
	}

	res.Success = true
	res.Message = "Koneksi berhasil dan vektor valid!"
	res.Dimensions = len(vec)
	if len(vec) > 3 {
		res.SampleVector = vec[:3]
	} else {
		res.SampleVector = vec
	}

	return res, nil
}

// CosineSimilarity calculates the cosine similarity between two float32 vectors.
// Returns a value between -1.0 and 1.0 (typically 0.0 to 1.0 for normalized embeddings).
func CosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		valA := float64(a[i])
		valB := float64(b[i])
		dot += valA * valB
		normA += valA * valA
		normB += valB * valB
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	sim := dot / (math.Sqrt(normA) * math.Sqrt(normB))
	if sim > 1.0 {
		sim = 1.0
	} else if sim < -1.0 {
		sim = -1.0
	}
	return float32(sim)
}
