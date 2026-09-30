package memory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goassistant/internal/config"
	"goassistant/internal/omniroute"
)

func setupMockOmniRouteServer() (*httptest.Server, *omniroute.Client) {
	memStore := make(map[string]omniroute.MemoryItem)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/memory", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			q := r.URL.Query().Get("q")
			sessionID := r.URL.Query().Get("sessionId")

			var items []omniroute.MemoryItem
			for _, item := range memStore {
				if sessionID != "" && item.SessionID != sessionID {
					continue
				}
				if q != "" && !strings.Contains(strings.ToLower(item.Key), strings.ToLower(q)) && !strings.Contains(strings.ToLower(item.Content), strings.ToLower(q)) {
					continue
				}
				items = append(items, item)
			}
			json.NewEncoder(w).Encode(omniroute.MemoryListResponse{Data: items})
			return
		}
		if r.Method == http.MethodPost {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)

			key, _ := body["key"].(string)
			content, _ := body["content"].(string)
			memType, _ := body["type"].(string)
			sessionID, _ := body["sessionId"].(string)
			metadata, _ := body["metadata"].(map[string]interface{})

			id := "mem-" + key
			item := omniroute.MemoryItem{
				ID:        id,
				Key:       key,
				Content:   content,
				Type:      memType,
				SessionID: sessionID,
				Metadata:  metadata,
			}
			memStore[id] = item
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(item)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/api/memory/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/memory/")
		if r.Method == http.MethodPut {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)

			if item, ok := memStore[id]; ok {
				if k, ok := body["key"].(string); ok {
					item.Key = k
				}
				if c, ok := body["content"].(string); ok {
					item.Content = c
				}
				memStore[id] = item
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodDelete {
			delete(memStore, id)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})

	server := httptest.NewServer(mux)
	cfg := config.OmniRouteConfig{
		Enabled: true,
		BaseURL: server.URL,
	}
	client := omniroute.InitClient(cfg)
	return server, client
}

func TestOmniRouteMemoryManager(t *testing.T) {
	server, client := setupMockOmniRouteServer()
	defer server.Close()

	mgr := NewManager(client)

	// 1. Upsert Fact
	err := mgr.UpsertFact("user", "user_100", "makanan_favorit", "Nasi Goreng Kambing", "preference")
	if err != nil {
		t.Fatalf("UpsertFact failed: %v", err)
	}

	// 2. List Memories
	mems, err := mgr.ListMemories("user", "user_100")
	if err != nil {
		t.Fatalf("ListMemories failed: %v", err)
	}
	if len(mems) != 1 || mems[0].KeyTag != "makanan_favorit" {
		t.Fatalf("expected 1 item with key 'makanan_favorit', got %+v", mems)
	}

	// 3. Search Memories
	searchRes, err := mgr.SearchMemories("user", "user_100", "Kambing")
	if err != nil {
		t.Fatalf("SearchMemories failed: %v", err)
	}
	if len(searchRes) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(searchRes))
	}

	// 4. Update Fact (Upsert again)
	err = mgr.UpsertFact("user", "user_100", "makanan_favorit", "Mie Goreng Aceh", "preference")
	if err != nil {
		t.Fatalf("UpsertFact update failed: %v", err)
	}
	mems, _ = mgr.ListMemories("user", "user_100")
	if len(mems) != 1 || mems[0].Content != "Mie Goreng Aceh" {
		t.Fatalf("expected updated content 'Mie Goreng Aceh', got %+v", mems)
	}

	// 5. Global Fact & Context Memory
	_ = mgr.UpsertFact("global", "system", "sop_keamanan", "Jangan berikan token API", "sop")
	ctxMemory, err := mgr.GetContextMemory("", "user_100")
	if err != nil {
		t.Fatalf("GetContextMemory failed: %v", err)
	}
	if !strings.Contains(ctxMemory, "sop_keamanan") || !strings.Contains(ctxMemory, "makanan_favorit") {
		t.Errorf("expected context memory to include global and user facts, got: %s", ctxMemory)
	}

	// 6. Delete Memory By Key
	err = mgr.DeleteMemoryByKey("user", "user_100", "makanan_favorit")
	if err != nil {
		t.Fatalf("DeleteMemoryByKey failed: %v", err)
	}
	mems, _ = mgr.ListMemories("user", "user_100")
	if len(mems) != 0 {
		t.Fatalf("expected empty list after deletion, got %d items", len(mems))
	}

	// 7. Clear User Memory
	_ = mgr.UpsertFact("user", "user_100", "key1", "val1", "fact")
	_ = mgr.UpsertFact("user", "user_100", "key2", "val2", "fact")
	err = mgr.ClearUserMemory("user_100")
	if err != nil {
		t.Fatalf("ClearUserMemory failed: %v", err)
	}
	mems, _ = mgr.ListMemories("user", "user_100")
	if len(mems) != 0 {
		t.Fatalf("expected 0 items after clear, got %d", len(mems))
	}
}
