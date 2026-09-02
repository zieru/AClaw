package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"goassistant/internal/storage"
)

const (
	DefaultWebshareBaseURL = "https://proxy.webshare.io/api/v2"
	DefaultWebshareGroup   = "webshare"
)

// WebshareProxyItem represents a single proxy object returned by Webshare API v2
type WebshareProxyItem struct {
	ID               string `json:"id"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	ProxyAddress     string `json:"proxy_address"`
	Port             int    `json:"port"`
	Valid            bool   `json:"valid"`
	LastVerification string `json:"last_verification"`
	CountryCode      string `json:"country_code"`
	CityName         string `json:"city_name"`
	ASNName          string `json:"asn_name"`
}

// WebshareListResponse represents paginated response from Webshare /api/v2/proxy/list/
type WebshareListResponse struct {
	Count    int                 `json:"count"`
	Next     *string             `json:"next"`
	Previous *string             `json:"previous"`
	Results  []WebshareProxyItem `json:"results"`
}

// WebshareProfile represents profile information from Webshare /api/v2/profile/
type WebshareProfile struct {
	Email          string `json:"email"`
	Subscribed     bool   `json:"subscribed"`
	ProxyCount     int    `json:"proxy_count"`
	BandwidthUsed  int64  `json:"bandwidth_used"`
	BandwidthLimit int64  `json:"bandwidth_limit"`
	CountryCode    string `json:"country_code"`
}

// WebshareClient handles communication with the Webshare.io API
type WebshareClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewWebshareClient creates a new client for Webshare API
func NewWebshareClient(apiKey string) *WebshareClient {
	return &WebshareClient{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: DefaultWebshareBaseURL,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

// SetBaseURL overrides the default Webshare API base URL (useful for testing)
func (c *WebshareClient) SetBaseURL(baseURL string) {
	c.baseURL = strings.TrimRight(baseURL, "/")
}

// SetHTTPClient sets custom HTTP client
func (c *WebshareClient) SetHTTPClient(client *http.Client) {
	if client != nil {
		c.httpClient = client
	}
}

// APIKey returns the configured API key
func (c *WebshareClient) APIKey() string {
	return c.apiKey
}

// newRequest creates an authenticated HTTP request to Webshare API
func (c *WebshareClient) newRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	reqURL := c.baseURL + endpoint
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Token "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "GoAssistant-ProxyPool/1.0")

	return req, nil
}

// GetProfile retrieves user profile info from Webshare
func (c *WebshareClient) GetProfile(ctx context.Context) (*WebshareProfile, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("Webshare API key belum dikonfigurasi")
	}

	req, err := c.newRequest(ctx, "GET", "/profile/", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi Webshare API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca respon profile Webshare: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Webshare API error (%d): %s", resp.StatusCode, string(body))
	}

	var prof WebshareProfile
	if err := json.Unmarshal(body, &prof); err != nil {
		return nil, fmt.Errorf("gagal unmarshal profile Webshare: %w", err)
	}

	return &prof, nil
}

// FetchProxies retrieves a single page of proxies from Webshare API
func (c *WebshareClient) FetchProxies(ctx context.Context, mode string, page, pageSize int, countries []string) (*WebshareListResponse, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("Webshare API key belum dikonfigurasi")
	}

	if mode == "" {
		mode = "direct"
	}
	if pageSize <= 0 {
		pageSize = 100
	}
	if page <= 0 {
		page = 1
	}

	params := url.Values{}
	params.Set("mode", mode)
	params.Set("page", strconv.Itoa(page))
	params.Set("page_size", strconv.Itoa(pageSize))

	if len(countries) > 0 {
		var validCodes []string
		for _, code := range countries {
			cTrim := strings.TrimSpace(code)
			if cTrim != "" {
				validCodes = append(validCodes, strings.ToUpper(cTrim))
			}
		}
		if len(validCodes) > 0 {
			params.Set("countries", strings.Join(validCodes, ","))
		}
	}

	endpoint := "/proxy/list/?" + params.Encode()
	req, err := c.newRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal request daftar proxy ke Webshare: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca respon proxy list Webshare: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Webshare API error (%d): %s", resp.StatusCode, string(body))
	}

	var listResp WebshareListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("gagal unmarshal proxy list Webshare: %w", err)
	}

	return &listResp, nil
}

// FetchAllProxies retrieves all pages of proxies from Webshare up to maxCount (or all if maxCount <= 0)
func (c *WebshareClient) FetchAllProxies(ctx context.Context, mode string, countries []string, maxCount int) ([]WebshareProxyItem, error) {
	var allItems []WebshareProxyItem
	page := 1
	pageSize := 100

	for {
		listResp, err := c.FetchProxies(ctx, mode, page, pageSize, countries)
		if err != nil {
			return allItems, err
		}

		allItems = append(allItems, listResp.Results...)

		if maxCount > 0 && len(allItems) >= maxCount {
			allItems = allItems[:maxCount]
			break
		}

		if listResp.Next == nil || len(listResp.Results) == 0 {
			break
		}

		page++
	}

	return allItems, nil
}

// BuildProxyURL formats a WebshareProxyItem into a standard URL (http://user:pass@host:port or socks5://...)
func BuildProxyURL(item WebshareProxyItem, protocol, mode string) string {
	if protocol == "" {
		protocol = "http"
	}
	protocol = strings.ToLower(protocol)

	host := item.ProxyAddress
	port := item.Port

	// In backbone mode, domain is always p.webshare.io
	if strings.ToLower(mode) == "backbone" {
		host = "p.webshare.io"
		if port == 0 {
			if protocol == "socks5" {
				port = 1080
			} else {
				port = 80
			}
		}
	}

	if item.Username != "" && item.Password != "" {
		escapedUser := url.QueryEscape(item.Username)
		escapedPass := url.QueryEscape(item.Password)
		return fmt.Sprintf("%s://%s:%s@%s:%d", protocol, escapedUser, escapedPass, host, port)
	}

	return fmt.Sprintf("%s://%s:%d", protocol, host, port)
}

// BuildProxyLabel generates a human-friendly label for a proxy node
func BuildProxyLabel(item WebshareProxyItem) string {
	parts := []string{"Webshare"}
	if item.CountryCode != "" {
		parts = append(parts, item.CountryCode)
	}
	if item.CityName != "" {
		parts = append(parts, item.CityName)
	}
	if item.ProxyAddress != "" {
		parts = append(parts, item.ProxyAddress)
	}
	return strings.Join(parts, " - ")
}

// SyncToPool fetches proxies from Webshare API and stores/updates them in the Proxy Pool
func (c *WebshareClient) SyncToPool(ctx context.Context, pool *Pool, groupName, protocol, mode string, countries []string, replaceExisting bool) (int, error) {
	if pool == nil {
		return 0, fmt.Errorf("proxy pool tidak boleh nil")
	}

	if groupName == "" {
		groupName = DefaultWebshareGroup
	}
	if protocol == "" {
		protocol = "http"
	}
	if mode == "" {
		mode = "direct"
	}

	items, err := c.FetchAllProxies(ctx, mode, countries, 0)
	if err != nil {
		return 0, fmt.Errorf("gagal mengambil proxy dari Webshare: %w", err)
	}

	if len(items) == 0 {
		return 0, fmt.Errorf("tidak ada proxy yang ditemukan di akun Webshare Anda")
	}

	if replaceExisting {
		_ = pool.DeleteGroup(groupName)
	}

	var records []*storage.ProxyNodeRecord
	for _, item := range items {
		rawURL := BuildProxyURL(item, protocol, mode)
		label := BuildProxyLabel(item)

		id := item.ID
		if id == "" {
			id = fmt.Sprintf("ws_%s_%d", strings.ReplaceAll(item.ProxyAddress, ".", "_"), item.Port)
		}
		if len(id) > 16 {
			id = id[:16]
		}

		records = append(records, &storage.ProxyNodeRecord{
			ID:        id,
			URL:       rawURL,
			Protocol:  protocol,
			Label:     label,
			GroupName: groupName,
			IsActive:  true,
		})
	}

	if pool.db != nil {
		if err := pool.db.SaveBatchProxies(records); err != nil {
			return 0, fmt.Errorf("gagal menyimpan proxy Webshare ke database: %w", err)
		}
		_ = pool.LoadFromDB()
	} else {
		pool.mu.Lock()
		pool.nodes = append(pool.nodes, records...)
		pool.mu.Unlock()
	}

	return len(records), nil
}

// StartAutoSync runs a periodic background sync of Webshare proxies to the pool
func (c *WebshareClient) StartAutoSync(ctx context.Context, pool *Pool, interval time.Duration, groupName, protocol, mode string, countries []string) {
	if interval < 5*time.Minute {
		interval = 60 * time.Minute
	}

	go func() {
		// Initial sync
		if count, err := c.SyncToPool(ctx, pool, groupName, protocol, mode, countries, false); err != nil {
			log.Printf("⚠️ [Webshare] Gagal inisialisasi sinkronisasi proxy: %v", err)
		} else {
			log.Printf("🌐 [Webshare] Berhasil sinkronisasi %d proxy ke group '%s'", count, groupName)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if count, err := c.SyncToPool(ctx, pool, groupName, protocol, mode, countries, true); err != nil {
					log.Printf("⚠️ [Webshare] Auto-sync gagal: %v", err)
				} else {
					log.Printf("🔄 [Webshare] Auto-sync berhasil: %d proxy diperbarui pada group '%s'", count, groupName)
				}
			}
		}
	}()
}
