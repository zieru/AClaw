package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/storage"
)

func setupTestStorage(t *testing.T) *storage.DB {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_embedder.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test storage: %v", err)
	}
	return db
}

func TestResolveAPIKey(t *testing.T) {
	db := setupTestStorage(t)
	defer db.Close()

	// 1. Direct config API key
	cfg := config.EmbeddingConfig{
		Enabled:  true,
		Provider: "gemini",
		Model:    "text-embedding-004",
		APIKey:   "AIzaSyManualTestKey",
	}
	emb := NewEmbedder(cfg, db)
	key, src := emb.ResolveAPIKey()
	if key != "AIzaSyManualTestKey" || src != "Pengaturan Memory Manual" {
		t.Errorf("expected manual key from config, got %s (%s)", key, src)
	}

	// 2. Prevent Gemini Web Scraper cookie from being selected as API key
	cfgNoKey := config.EmbeddingConfig{
		Enabled:  true,
		Provider: "gemini",
		Model:    "text-embedding-004",
		APIKey:   "",
	}
	err := db.SaveProvider(&storage.ProviderRecord{
		ID:        "p_gemini_web",
		Name:      "Gemini Web (Google Auth)",
		Type:      "gemini_web",
		APIKey:    "__Secure-1PSID=cookie123; __Secure-1PSIDTS=cookie456",
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to save gemini_web provider: %v", err)
	}

	embWebOnly := NewEmbedder(cfgNoKey, db)
	keyWeb, srcWeb := embWebOnly.ResolveAPIKey()
	if keyWeb != "" {
		t.Errorf("expected empty key when only gemini_web scraper is present, got %s", keyWeb)
	}
	if !strings.Contains(srcWeb, "web scraper cookie") {
		t.Errorf("expected warning about web scraper cookie, got: %s", srcWeb)
	}

	// 3. Fallback to legitimate DB provider
	err = db.SaveProvider(&storage.ProviderRecord{
		ID:        "p_gemini_official",
		Name:      "Google Gemini Official",
		Type:      "gemini",
		APIKey:    "AIzaSyOfficialKey1234",
		IsActive:  true,
		CreatedAt: time.Now().Add(time.Second),
		UpdatedAt: time.Now().Add(time.Second),
	})
	if err != nil {
		t.Fatalf("failed to save official gemini provider: %v", err)
	}

	embDB := NewEmbedder(cfgNoKey, db)
	keyDB, srcDB := embDB.ResolveAPIKey()
	if keyDB != "AIzaSyOfficialKey1234" || srcDB != "Provider Database 'Google Gemini Official'" {
		t.Errorf("expected official key from db provider, got %s (%s)", keyDB, srcDB)
	}

	// 4. Fallback to Environment variable
	cfgNoKeyOpenAI := config.EmbeddingConfig{
		Enabled:  true,
		Provider: "openai",
		Model:    "text-embedding-3-small",
		APIKey:   "",
	}
	os.Setenv("OPENAI_API_KEY", "sk-proj-env-openai-key-5678")
	defer os.Unsetenv("OPENAI_API_KEY")

	embEnv := NewEmbedder(cfgNoKeyOpenAI, db)
	keyEnv, srcEnv := embEnv.ResolveAPIKey()
	if keyEnv != "sk-proj-env-openai-key-5678" || srcEnv != "Environment (OPENAI_API_KEY)" {
		t.Errorf("expected key from env var, got %s (%s)", keyEnv, srcEnv)
	}
}

func TestGeminiNativeEmbedding(t *testing.T) {
	// Mock Gemini API Server
	var receivedPath string
	var receivedQueryKey string
	var receivedHeaderKey string
	var singleReq geminiEmbedContentRequest
	var batchReq geminiBatchEmbedContentsRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedQueryKey = r.URL.Query().Get("key")
		receivedHeaderKey = r.Header.Get("x-goog-api-key")

		if r.URL.Path == "/v1beta/models/text-embedding-004:embedContent" {
			_ = json.NewDecoder(r.Body).Decode(&singleReq)
			resp := geminiEmbedContentResponse{
				Embedding: &struct {
					Values []float32 `json:"values"`
				}{
					Values: []float32{0.1, 0.2, 0.3, 0.4},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if r.URL.Path == "/v1beta/models/text-embedding-004:batchEmbedContents" {
			_ = json.NewDecoder(r.Body).Decode(&batchReq)
			resp := geminiBatchEmbedContentsResponse{
				Embeddings: []struct {
					Values []float32 `json:"values"`
				}{
					{Values: []float32{0.1, 0.2, 0.3, 0.4}},
					{Values: []float32{0.5, 0.6, 0.7, 0.8}},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	cfg := config.EmbeddingConfig{
		Enabled:    true,
		Provider:   "gemini",
		Model:      "text-embedding-004",
		BaseURL:    server.URL,
		APIKey:     "secret-gemini-test-key",
		Dimensions: 4,
	}

	emb := NewEmbedder(cfg, nil)

	// Test Single Embed
	vec, err := emb.Embed(context.Background(), "Halo dunia")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(vec) != 4 || vec[0] != 0.1 || vec[3] != 0.4 {
		t.Errorf("unexpected vector: %v", vec)
	}
	if receivedPath != "/v1beta/models/text-embedding-004:embedContent" {
		t.Errorf("unexpected path: %s", receivedPath)
	}
	if receivedQueryKey != "secret-gemini-test-key" {
		t.Errorf("unexpected query api key: %s", receivedQueryKey)
	}
	if receivedHeaderKey != "secret-gemini-test-key" {
		t.Errorf("unexpected header x-goog-api-key: %s", receivedHeaderKey)
	}
	if len(singleReq.Content.Parts) == 0 || singleReq.Content.Parts[0].Text != "Halo dunia" {
		t.Errorf("unexpected content text: %+v", singleReq.Content)
	}

	// Test Batch Embed
	batchVecs, err := emb.EmbedBatch(context.Background(), []string{"Item 1", "Item 2"})
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	if len(batchVecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(batchVecs))
	}
	if len(batchReq.Requests) != 2 {
		t.Fatalf("expected 2 requests in batch, got %d", len(batchReq.Requests))
	}
	if len(batchReq.Requests[0].Content.Parts) == 0 || batchReq.Requests[0].Content.Parts[0].Text != "Item 1" {
		t.Errorf("unexpected batch request 0 text: %+v", batchReq.Requests[0].Content)
	}
}

func TestEmbedderTestConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiEmbedContentResponse{
			Embedding: &struct {
				Values []float32 `json:"values"`
			}{
				Values: []float32{0.01, 0.02, 0.03},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := config.EmbeddingConfig{
		Enabled:    true,
		Provider:   "gemini",
		Model:      "text-embedding-004",
		BaseURL:    server.URL,
		APIKey:     "test-key",
		Dimensions: 3,
	}

	emb := NewEmbedder(cfg, nil)
	res, err := emb.TestConnection(context.Background())
	if err != nil {
		t.Fatalf("TestConnection failed: %v", err)
	}
	if !res.Success {
		t.Errorf("expected success, got %v (%s)", res.Success, res.Message)
	}
	if res.Dimensions != 3 {
		t.Errorf("expected 3 dimensions, got %d", res.Dimensions)
	}
	if res.StatusCode != 200 {
		t.Errorf("expected 200 status code, got %d", res.StatusCode)
	}
}
