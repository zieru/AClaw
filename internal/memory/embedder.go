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
	"sync"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/storage"
)

// GeminiModelDetails represents model info returned by Google AI /v1beta/models
type GeminiModelDetails struct {
	Name                       string   `json:"name"`                       // e.g. "models/gemini-embedding-001"
	DisplayName                string   `json:"displayName"`                // e.g. "Gemini Embedding 001"
	Description                string   `json:"description,omitempty"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"` // ["embedContent", ...]
}

// EmbedTestResult contains diagnostic details from a live connection test
type EmbedTestResult struct {
	Success         bool                 `json:"success"`
	StatusCode      int                  `json:"status_code"`
	Provider        string               `json:"provider"`
	Model           string               `json:"model"`
	Endpoint        string               `json:"endpoint"`
	KeySource       string               `json:"key_source"`
	Latency         time.Duration        `json:"latency"`
	Dimensions      int                  `json:"dimensions"`
	SampleVector    []float32            `json:"sample_vector,omitempty"`
	AvailableModels []GeminiModelDetails `json:"available_models,omitempty"`
	Message         string               `json:"message,omitempty"`
	ErrorMessage    string               `json:"error_message,omitempty"`
}

// Embedder generates vector embeddings for text
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	IsEnabled() bool
	Dimensions() int
	TestConnection(ctx context.Context) (*EmbedTestResult, error)
	ResolveAPIKey() (string, string)
	FetchAvailableEmbeddingModels(ctx context.Context) ([]GeminiModelDetails, error)
}

// ClientEmbedder implements Embedder dedicated to Google Gemini
type ClientEmbedder struct {
	cfg          config.EmbeddingConfig
	db           *storage.DB
	httpClient   *http.Client
	mu           sync.RWMutex
	cachedModels []GeminiModelDetails
	cacheExpiry  time.Time
}

// NewEmbedder creates a new Embedder instance based on config and optional storage DB
func NewEmbedder(cfg config.EmbeddingConfig, db *storage.DB) Embedder {
	cfg.Provider = "gemini"
	if cfg.Model == "" || cfg.Model == "text-embedding-004" || cfg.Model == "models/text-embedding-004" {
		cfg.Model = "gemini-embedding-001"
	}
	if cfg.Dimensions <= 0 {
		cfg.Dimensions = 768
	}
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
		return 768
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

// ResolveAPIKey determines the Google Gemini API key to use and describes its source
func (e *ClientEmbedder) ResolveAPIKey() (string, string) {
	if e == nil {
		return "", "None"
	}
	// 1. Explicitly configured API Key in memory settings
	if strings.TrimSpace(e.cfg.APIKey) != "" {
		manualKey := strings.TrimSpace(e.cfg.APIKey)
		if isCookieOrInvalidKey(manualKey) {
			return manualKey, "Kunci Manual (Peringatan: Berupa cookie web, bukan Google AI Studio API key resmi)"
		}
		return manualKey, "Pengaturan Memory Manual"
	}

	hasGeminiWebOnly := false

	// 2. Query GoAssistant database for an active Google Gemini provider
	if e.db != nil {
		if providers, err := e.db.ListProviders(); err == nil {
			for _, p := range providers {
				pType := strings.ToLower(strings.TrimSpace(p.Type))
				pName := strings.ToLower(strings.TrimSpace(p.Name))

				// Never treat web scrapers as REST embedding providers
				if pType == "gemini_web" || strings.Contains(pType, "web") || strings.Contains(pType, "scrape") {
					if p.IsActive {
						hasGeminiWebOnly = true
					}
					continue
				}

				isMatch := false
				if pType == "gemini" {
					isMatch = true
				} else if strings.Contains(pName, "gemini") && !strings.Contains(pName, "web") && !strings.Contains(pName, "scrape") {
					isMatch = true
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

	// 3. Fallback to Environment Variables (GEMINI_API_KEY)
	if envKey := os.Getenv("GEMINI_API_KEY"); envKey != "" && !isCookieOrInvalidKey(envKey) {
		return envKey, "Environment (GEMINI_API_KEY)"
	}

	if hasGeminiWebOnly {
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

// EmbedBatch generates embeddings for multiple text strings using Google Gemini
func (e *ClientEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if !e.IsEnabled() || len(texts) == 0 {
		return nil, nil
	}
	return e.embedGemini(ctx, texts)
}

// FetchAvailableEmbeddingModels queries Google AI Studio API for models supporting embedContent
func (e *ClientEmbedder) FetchAvailableEmbeddingModels(ctx context.Context) ([]GeminiModelDetails, error) {
	if e == nil {
		return nil, fmt.Errorf("embedder is nil")
	}

	e.mu.RLock()
	if len(e.cachedModels) > 0 && time.Now().Before(e.cacheExpiry) {
		cached := make([]GeminiModelDetails, len(e.cachedModels))
		copy(cached, e.cachedModels)
		e.mu.RUnlock()
		return cached, nil
	}
	e.mu.RUnlock()

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

	reqURL := fmt.Sprintf("%s/v1beta/models?key=%s", baseURL, url.QueryEscape(apiKey))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create list models request: %w", err)
	}
	httpReq.Header.Set("x-goog-api-key", apiKey)

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call list models api: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		bodyStr := string(bodyBytes)
		if strings.Contains(bodyStr, "<html") || strings.Contains(bodyStr, "<!DOCTYPE") {
			return nil, fmt.Errorf("gemini api error (HTTP %d): Bad Request dari Google Front End (periksa kembali API key Google AI Studio Anda)", resp.StatusCode)
		}
		return nil, fmt.Errorf("gemini list models error (%d): %s", resp.StatusCode, bodyStr)
	}

	var listResp struct {
		Models []GeminiModelDetails `json:"models"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error,omitempty"`
	}

	if err := json.Unmarshal(bodyBytes, &listResp); err != nil {
		return nil, fmt.Errorf("unmarshal list models response: %w", err)
	}

	if listResp.Error != nil && listResp.Error.Message != "" {
		return nil, fmt.Errorf("gemini list models error: %s", listResp.Error.Message)
	}

	var embeddingModels []GeminiModelDetails
	for _, m := range listResp.Models {
		for _, method := range m.SupportedGenerationMethods {
			if strings.EqualFold(method, "embedContent") {
				embeddingModels = append(embeddingModels, m)
				break
			}
		}
	}

	if len(embeddingModels) == 0 {
		return nil, fmt.Errorf("tidak ditemukan model Gemini dengan dukungan embedContent dari akun Google API ini")
	}

	e.mu.Lock()
	e.cachedModels = embeddingModels
	e.cacheExpiry = time.Now().Add(1 * time.Hour)
	e.mu.Unlock()

	return embeddingModels, nil
}

// --- Google Gemini Native Implementation ---

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiEmbedContentRequest struct {
	Model                string        `json:"model"`
	Content              geminiContent `json:"content"`
	OutputDimensionality int           `json:"outputDimensionality,omitempty"`
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
	if m == "" || m == "text-embedding-004" || m == "models/text-embedding-004" {
		return "models/gemini-embedding-001"
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
			OutputDimensionality: e.Dimensions(),
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
			OutputDimensionality: e.Dimensions(),
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

// TestConnection verifies live connectivity with Google Gemini embedding endpoint and detects available models
func (e *ClientEmbedder) TestConnection(ctx context.Context) (*EmbedTestResult, error) {
	if e == nil {
		return nil, fmt.Errorf("embedder is nil")
	}

	_, keySource := e.ResolveAPIKey()

	// 1. Fetch available models from Google API
	availModels, fetchErr := e.FetchAvailableEmbeddingModels(ctx)

	// Determine active model
	activeModel := e.cfg.Model
	if activeModel == "" || activeModel == "text-embedding-004" || activeModel == "models/text-embedding-004" {
		activeModel = "gemini-embedding-001"
		if len(availModels) > 0 {
			cleanName := strings.TrimPrefix(availModels[0].Name, "models/")
			activeModel = cleanName
		}
		e.cfg.Model = activeModel
	}

	// If current model is not found in discovered models, auto-switch to first available supported model
	if len(availModels) > 0 {
		found := false
		normCurrent := normalizeGeminiModel(activeModel)
		for _, m := range availModels {
			if strings.EqualFold(m.Name, normCurrent) || strings.EqualFold(strings.TrimPrefix(m.Name, "models/"), activeModel) {
				found = true
				break
			}
		}
		if !found {
			// Auto-fallback to the first supported model
			cleanName := strings.TrimPrefix(availModels[0].Name, "models/")
			activeModel = cleanName
			e.cfg.Model = activeModel
		}
	}

	baseURL := strings.TrimRight(e.cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	endpointDesc := fmt.Sprintf("%s/v1beta/%s:embedContent", baseURL, normalizeGeminiModel(activeModel))

	start := time.Now()
	vec, err := e.Embed(ctx, "GoAssistant Memory Diagnostic Test")
	latency := time.Since(start)

	res := &EmbedTestResult{
		Provider:        "gemini",
		Model:           activeModel,
		Endpoint:        endpointDesc,
		KeySource:       keySource,
		Latency:         latency,
		StatusCode:      200,
		AvailableModels: availModels,
	}

	if err != nil {
		res.Success = false
		res.ErrorMessage = err.Error()
		res.Message = err.Error()
		res.StatusCode = 500
		if fetchErr != nil && strings.Contains(fetchErr.Error(), "API key") {
			res.Message = fetchErr.Error()
		}
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
