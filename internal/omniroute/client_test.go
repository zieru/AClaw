package omniroute

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"goassistant/internal/config"
)

func TestOmniRouteClient(t *testing.T) {
	mux := http.NewServeMux()

	// 1. Mock Login
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req["password"] != "testpass" {
			http.Error(w, "invalid password", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:  "auth_token",
			Value: "test-auth-token-123",
			Path:  "/",
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
	})

	// Helper to check cookie
	checkAuth := func(r *http.Request) bool {
		c, err := r.Cookie("auth_token")
		return err == nil && c.Value == "test-auth-token-123"
	}

	// 2. Mock Health
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{
			Status:    "ok",
			Timestamp: time.Now().Format(time.RFC3339),
		})
	})

	// 3. Mock Analytics
	mux.HandleFunc("/api/usage/analytics", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(AnalyticsResponse{
			Summary: AnalyticsSummary{
				TotalRequests:    700,
				PromptTokens:     1000000,
				CompletionTokens: 50000,
				TotalTokens:      1050000,
				AvgLatencyMs:     1200,
				TotalCost:        1.50,
			},
		})
	})

	// 4. Mock Request Logs
	mux.HandleFunc("/api/usage/request-logs", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]string{
			"2026-09-28T03:00:00Z | test | OK",
		})
	})

	// 5. Mock Search
	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SearchResponse{
			ID:       "search-123",
			Provider: "tavily-search",
			Query:    req["query"],
			Results: []SearchResultItem{
				{
					Title:   "Test Title",
					URL:     "https://example.com/test",
					Snippet: "Test snippet content",
				},
			},
		})
	})

	// 6. Mock Memory
	mux.HandleFunc("/api/memory", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(MemoryListResponse{
				Data: []MemoryItem{
					{
						ID:      "mem-1",
						Type:    "factual",
						Key:     "test_key",
						Content: "test content",
					},
				},
			})
			return
		}
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(MemoryItem{
				ID:      "mem-new",
				Type:    "factual",
				Key:     "created_key",
				Content: "created content",
			})
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/api/memory/", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/memory/")
		if id == "" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(MemoryItem{
				ID:      id,
				Type:    "factual",
				Key:     "test_key",
				Content: "test content",
			})
		case http.MethodPut:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]bool{"success": true})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	cfg := config.OmniRouteConfig{
		Enabled:  true,
		BaseURL:  server.URL,
		Password: "testpass",
	}

	client := InitClient(cfg)
	ctx := context.Background()

	// Test Health
	health, err := client.GetHealth(ctx)
	if err != nil || health.Status != "ok" {
		t.Fatalf("GetHealth failed: %v", err)
	}

	// Test Analytics
	analytics, err := client.GetAnalytics(ctx)
	if err != nil || analytics.Summary.TotalRequests != 700 {
		t.Fatalf("GetAnalytics failed: %v", err)
	}

	// Test Request Logs
	logs, err := client.GetRequestLogs(ctx)
	if err != nil || len(logs) == 0 {
		t.Fatalf("GetRequestLogs failed: %v", err)
	}

	// Test Search
	searchRes, err := client.Search(ctx, "golang")
	if err != nil || len(searchRes.Results) == 0 {
		t.Fatalf("Search failed: %v", err)
	}
	if searchRes.Results[0].Title != "Test Title" {
		t.Errorf("Expected title 'Test Title', got '%s'", searchRes.Results[0].Title)
	}

	// Test ListMemories
	mems, err := client.ListMemories(ctx)
	if err != nil || len(mems) == 0 {
		t.Fatalf("ListMemories failed: %v", err)
	}
	if mems[0].Key != "test_key" {
		t.Errorf("Expected key 'test_key', got '%s'", mems[0].Key)
	}

	// Test GetMemory
	item, err := client.GetMemory(ctx, "mem-1")
	if err != nil || item.ID != "mem-1" {
		t.Fatalf("GetMemory failed: %v", err)
	}

	// Test UpdateMemory
	err = client.UpdateMemory(ctx, "mem-1", "test_key", "updated content", "factual", nil)
	if err != nil {
		t.Fatalf("UpdateMemory failed: %v", err)
	}

	// Test DeleteMemory
	err = client.DeleteMemory(ctx, "mem-1")
	if err != nil {
		t.Fatalf("DeleteMemory failed: %v", err)
	}

	// Test SearchMemories
	searchMems, err := client.SearchMemories(ctx, "test", "")
	if err != nil || len(searchMems) == 0 {
		t.Fatalf("SearchMemories failed: %v", err)
	}

	// Test UpsertMemory
	err = client.UpsertMemory(ctx, "test_key", "upserted content", "factual", "", nil)
	if err != nil {
		t.Fatalf("UpsertMemory failed: %v", err)
	}

	// Test SaveMemory
	err = client.SaveMemory(ctx, "new_key", "new content", "factual", nil)
	if err != nil {
		t.Fatalf("SaveMemory failed: %v", err)
	}
}
