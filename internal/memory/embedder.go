package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"goassistant/internal/config"
)

// Embedder generates vector embeddings for text
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	IsEnabled() bool
	Dimensions() int
}

// ClientEmbedder implements Embedder using standard OpenAI-compatible /v1/embeddings endpoint
type ClientEmbedder struct {
	cfg        config.EmbeddingConfig
	httpClient *http.Client
}

// NewEmbedder creates a new Embedder instance based on config
func NewEmbedder(cfg config.EmbeddingConfig) Embedder {
	return &ClientEmbedder{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (e *ClientEmbedder) IsEnabled() bool {
	return e != nil && e.cfg.Enabled && e.cfg.Model != ""
}

func (e *ClientEmbedder) Dimensions() int {
	if e == nil || e.cfg.Dimensions <= 0 {
		return 1536
	}
	return e.cfg.Dimensions
}

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

	baseURL := strings.TrimRight(e.cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if !strings.HasSuffix(baseURL, "/embeddings") {
		baseURL = baseURL + "/embeddings"
	}

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
	if e.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)
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
