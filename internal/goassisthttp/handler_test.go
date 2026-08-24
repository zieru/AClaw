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

	foundVisit := false
	foundPivot := false
	for _, ep := range cfg.Endpoints {
		if ep.Path == "/api/visit" {
			foundVisit = true
			if ep.Method != "GET" {
				t.Errorf("expected GET method, got %s", ep.Method)
			}
			if ep.Binary != "g3a" {
				t.Errorf("expected binary g3a, got %s", ep.Binary)
			}
			if ep.Type != "regular" {
				t.Errorf("expected type regular, got %s", ep.Type)
			}
		}
		if ep.Path == "/api/visit/pivot" {
			foundPivot = true
			if ep.Defaults["pivot"] != "status_layanan" {
				t.Errorf("expected pivot on status_layanan, got %s", ep.Defaults["pivot"])
			}
		}
	}

	if !foundVisit {
		t.Errorf("endpoint /api/visit tidak ditemukan di endpoints.yaml")
	}
	if !foundPivot {
		t.Errorf("endpoint /api/visit/pivot tidak ditemukan di endpoints.yaml")
	}

}


