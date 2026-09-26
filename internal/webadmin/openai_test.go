package webadmin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"goassistant/internal/config"
)

func TestAuthManager_BearerAPIKey(t *testing.T) {
	cfg := &config.AppConfig{}
	cfg.WebAdmin.APIKey = "sk-custom-test-key-12345"

	auth := NewAuthManager(cfg, nil, nil)

	// 1. Initial key must have sk- prefix
	if auth.GetAPIKey() != "sk-custom-test-key-12345" {
		t.Fatalf("expected API key 'sk-custom-test-key-12345', got '%s'", auth.GetAPIKey())
	}

	// 2. Protected handler
	called := false
	handler := auth.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		sess := GetSessionFromContext(r.Context())
		if sess == nil || !sess.IsAPIKey {
			t.Errorf("expected session with IsAPIKey=true")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// 3. Request without token should fail (401)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rr.Code)
	}
	if called {
		t.Fatalf("handler should not have been called")
	}

	// 4. Request with wrong token should fail (401)
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-wrong-key")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rr.Code)
	}

	// 5. Request with correct token should succeed (200)
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-custom-test-key-12345")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
	if !called {
		t.Fatalf("handler should have been called")
	}

	// 6. Test SetAPIKey normalization (adds sk- prefix if missing)
	newKey, err := auth.SetAPIKey("my-new-token-999")
	if err != nil {
		t.Fatalf("unexpected error setting key: %v", err)
	}
	if newKey != "sk-my-new-token-999" {
		t.Fatalf("expected 'sk-my-new-token-999', got '%s'", newKey)
	}

	// 7. Test GenerateAPIKey
	genKey, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("unexpected error generating key: %v", err)
	}
	if len(genKey) < 20 || genKey[:12] != "sk-goassist-" {
		t.Fatalf("expected prefix 'sk-goassist-', got '%s'", genKey)
	}
}

func TestServer_OpenAIModelsAndAPIKeyEndpoints(t *testing.T) {
	cfg := &config.AppConfig{}
	cfg.WebAdmin.APIKey = "sk-test-openai-12345"

	srv := NewServer(cfg, nil, nil, nil)
	mux := srv.buildRoutes()

	// 1. GET /v1/models with valid Bearer token
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-test-openai-12345")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /v1/models, got %d: %s", rr.Code, rr.Body.String())
	}

	var modelList OpenAIModelListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &modelList); err != nil {
		t.Fatalf("failed to decode model list JSON: %v", err)
	}
	if modelList.Object != "list" {
		t.Fatalf("expected object 'list', got '%s'", modelList.Object)
	}

	// 2. GET /api/system/apikey
	req = httptest.NewRequest(http.MethodGet, "/api/system/apikey", nil)
	req.Header.Set("Authorization", "Bearer sk-test-openai-12345")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for GET /api/system/apikey, got %d", rr.Code)
	}

	// 3. POST /api/system/apikey (generate new key)
	genPayload := map[string]interface{}{"generate": true}
	body, _ := json.Marshal(genPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/system/apikey", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test-openai-12345")
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for POST /api/system/apikey, got %d: %s", rr.Code, rr.Body.String())
	}

	var genResp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &genResp)
	updatedKey, ok := genResp["api_key"].(string)
	if !ok || len(updatedKey) < 15 || updatedKey[:12] != "sk-goassist-" {
		t.Fatalf("expected newly generated key with sk-goassist-, got '%v'", genResp["api_key"])
	}
}
