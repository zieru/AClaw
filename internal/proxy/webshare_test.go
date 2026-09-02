package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildProxyURL(t *testing.T) {
	item := WebshareProxyItem{
		Username:     "usr1",
		Password:     "pass1",
		ProxyAddress: "192.168.1.50",
		Port:         8000,
		CountryCode:  "US",
	}

	// Test Direct HTTP
	uDirect := BuildProxyURL(item, "http", "direct")
	expectedDirect := "http://usr1:pass1@192.168.1.50:8000"
	if uDirect != expectedDirect {
		t.Errorf("expected %s, got %s", expectedDirect, uDirect)
	}

	// Test Direct SOCKS5
	uSocks := BuildProxyURL(item, "socks5", "direct")
	expectedSocks := "socks5://usr1:pass1@192.168.1.50:8000"
	if uSocks != expectedSocks {
		t.Errorf("expected %s, got %s", expectedSocks, uSocks)
	}

	// Test Backbone mode
	uBackbone := BuildProxyURL(item, "http", "backbone")
	expectedBackbone := "http://usr1:pass1@p.webshare.io:8000"
	if uBackbone != expectedBackbone {
		t.Errorf("expected %s, got %s", expectedBackbone, uBackbone)
	}
}

func TestWebshareClient_GetProfileAndFetch(t *testing.T) {
	// Mock Webshare API server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Token test-token-123" {
			http.Error(w, `{"detail":"Invalid token"}`, http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/profile/":
			resp := WebshareProfile{
				Email:      "user@example.com",
				Subscribed: true,
				ProxyCount: 10,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case "/proxy/list/":
			page := r.URL.Query().Get("page")
			if page == "1" {
				nextPage := "http://" + r.Host + "/proxy/list/?page=2"
				resp := WebshareListResponse{
					Count: 2,
					Next:  &nextPage,
					Results: []WebshareProxyItem{
						{
							ID:           "node_1",
							Username:     "user1",
							Password:     "pass1",
							ProxyAddress: "10.0.0.1",
							Port:         8000,
							CountryCode:  "US",
							CityName:     "New York",
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			} else {
				resp := WebshareListResponse{
					Count: 2,
					Next:  nil,
					Results: []WebshareProxyItem{
						{
							ID:           "node_2",
							Username:     "user2",
							Password:     "pass2",
							ProxyAddress: "10.0.0.2",
							Port:         8000,
							CountryCode:  "SG",
							CityName:     "Singapore",
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	client := NewWebshareClient("test-token-123")
	client.SetBaseURL(mockServer.URL)
	client.SetHTTPClient(mockServer.Client())

	ctx := context.Background()

	// 1. Test GetProfile
	prof, err := client.GetProfile(ctx)
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}
	if prof.Email != "user@example.com" || prof.ProxyCount != 10 {
		t.Errorf("unexpected profile data: %+v", prof)
	}

	// 2. Test FetchAllProxies pagination
	items, err := client.FetchAllProxies(ctx, "direct", nil, 0)
	if err != nil {
		t.Fatalf("FetchAllProxies failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items across pages, got %d", len(items))
	}
	if items[0].CountryCode != "US" || items[1].CountryCode != "SG" {
		t.Errorf("unexpected items: %+v", items)
	}

	// 3. Test SyncToPool
	pool := NewPool(nil, true, "round-robin")
	count, err := client.SyncToPool(ctx, pool, "webshare_group", "http", "direct", nil, false)
	if err != nil {
		t.Fatalf("SyncToPool failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 proxies synced, got %d", count)
	}

	nodes := pool.ListNodesByGroup("webshare_group")
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes in pool group, got %d", len(nodes))
	}

	// Verify URL format
	if nodes[0].URL != "http://user1:pass1@10.0.0.1:8000" {
		t.Errorf("unexpected proxy URL: %s", nodes[0].URL)
	}
}
