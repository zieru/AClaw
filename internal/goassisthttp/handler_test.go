package goassisthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDynamicHandler_MethodNotAllowed(t *testing.T) {
	ep := EndpointItem{
		Path:    "/api/test",
		Method:  "GET",
		Binary:  "echo",
		Command: "hello",
		Type:    "regular",
	}

	handler := CreateDynamicHandler(ep)
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	res := w.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, res.StatusCode)
	}

	var resp APIResponse
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "error" {
		t.Errorf("expected status error, got %s", resp.Status)
	}
}

func TestHealthEndpoint(t *testing.T) {
	server := NewServer(0, "nonexistent.yaml", 0, 0)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, res.StatusCode)
	}
}

func TestRoutesDiscoveryEndpoint(t *testing.T) {
	server := NewServer(0, "nonexistent.yaml", 0, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, res.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		t.Fatalf("failed to decode routes discovery: %v", err)
	}

	if data["status"] != "success" {
		t.Errorf("expected status success, got %v", data["status"])
	}
}

func TestLoadEndpointsConfig_VisitEndpoint(t *testing.T) {
	cfg, err := LoadEndpointsConfig("../../configs/endpoints.yaml")
	if err != nil {
		t.Fatalf("gagal memuat endpoints.yaml: %v", err)
	}

	expectedPaths := map[string]bool{
		"/api/visit/kpi":      false,
		"/api/visit/mitra":    false,
		"/api/visit/trend":    false,
		"/api/visit/service":  false,
		"/api/visit/regional": false,
		"/api/visit/grapari":  false,
		"/api/visit/mom":      false,
	}

	for _, ep := range cfg.Endpoints {
		if _, ok := expectedPaths[ep.Path]; ok {
			expectedPaths[ep.Path] = true
			if ep.Method != "GET" {
				t.Errorf("expected GET method for %s, got %s", ep.Path, ep.Method)
			}
			if ep.Binary != "g3a" {
				t.Errorf("expected binary g3a for %s, got %s", ep.Path, ep.Binary)
			}
			if ep.Type != "regular" {
				t.Errorf("expected type regular for %s, got %s", ep.Path, ep.Type)
			}
		}
	}

	for path, found := range expectedPaths {
		if !found {
			t.Errorf("endpoint %s tidak ditemukan di endpoints.yaml", path)
		}
	}
}



