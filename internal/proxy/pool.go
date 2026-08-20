package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"goassistant/internal/storage"
)

// Pool manages a list of upstream proxies for AI providers and tools
type Pool struct {
	mu           sync.RWMutex
	db           *storage.DB
	nodes        []*storage.ProxyNodeRecord
	currentIndex int
	strategy     string // "round-robin", "random", "least-errors", "best-latency"
	enabled      bool
}

// NewPool creates a new proxy pool instance
func NewPool(db *storage.DB, enabled bool, strategy string) *Pool {
	if strategy == "" {
		strategy = "round-robin"
	}
	p := &Pool{
		db:       db,
		strategy: strings.ToLower(strategy),
		enabled:  enabled,
	}
	_ = p.LoadFromDB()
	return p
}

// LoadFromDB refreshes proxy nodes from the database
func (p *Pool) LoadFromDB() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.db == nil {
		return nil
	}

	records, err := p.db.ListProxyNodes()
	if err != nil {
		return err
	}

	nodes := make([]*storage.ProxyNodeRecord, len(records))
	for i := range records {
		rec := records[i]
		nodes[i] = &rec
	}
	p.nodes = nodes
	return nil
}

// SetEnabled enables or disables the proxy pool
func (p *Pool) SetEnabled(enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.enabled = enabled
}

// IsEnabled returns whether the proxy pool is active
func (p *Pool) IsEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.enabled && len(p.nodes) > 0
}

// SetStrategy sets the node selection strategy
func (p *Pool) SetStrategy(strategy string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.strategy = strings.ToLower(strategy)
}

// GetStrategy returns current selection strategy
func (p *Pool) GetStrategy() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.strategy
}

// AddNode adds a new proxy URL to pool and database in default group
func (p *Pool) AddNode(rawURL, label string) (*storage.ProxyNodeRecord, error) {
	return p.AddNodeWithGroup(rawURL, label, "default")
}

// AddNodeWithGroup adds a new proxy URL to a specific group
func (p *Pool) AddNodeWithGroup(rawURL, label, group string) (*storage.ProxyNodeRecord, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("URL proxy tidak boleh kosong")
	}
	if group == "" {
		group = "default"
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("URL proxy tidak valid: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "socks5" {
		return nil, fmt.Errorf("protokol '%s' tidak didukung (harus http, https, atau socks5)", scheme)
	}

	if label == "" {
		label = parsed.Host
	}

	record := &storage.ProxyNodeRecord{
		ID:        uuid.New().String()[:8],
		URL:       rawURL,
		Protocol:  scheme,
		Label:     label,
		GroupName: group,
		IsActive:  true,
	}

	if p.db != nil {
		if err := p.db.SaveProxyNode(record); err != nil {
			return nil, err
		}
		_ = p.LoadFromDB()
	} else {
		p.mu.Lock()
		p.nodes = append(p.nodes, record)
		p.mu.Unlock()
	}

	return record, nil
}

// AddBatch parses and inserts a batch of proxy URLs into a specific group
func (p *Pool) AddBatch(rawProxies []string, group string) (int, error) {
	if group == "" {
		group = "default"
	}

	var records []*storage.ProxyNodeRecord
	for _, item := range rawProxies {
		tokens := strings.FieldsFunc(item, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
		})

		for _, raw := range tokens {
			raw = strings.TrimSpace(raw)
			if raw == "" || strings.HasPrefix(raw, "#") {
				continue
			}

			// Support format without scheme, default to http://
			if !strings.Contains(raw, "://") {
				raw = "http://" + raw
			}

			parsed, err := url.Parse(raw)
			if err != nil {
				continue
			}

			scheme := strings.ToLower(parsed.Scheme)
			if scheme != "http" && scheme != "https" && scheme != "socks5" {
				continue
			}

			records = append(records, &storage.ProxyNodeRecord{
				ID:        uuid.New().String()[:8],
				URL:       raw,
				Protocol:  scheme,
				Label:     parsed.Host,
				GroupName: group,
				IsActive:  true,
			})
		}
	}

	if len(records) == 0 {
		return 0, fmt.Errorf("tidak ada URL proxy valid yang dapat ditambahkan")
	}

	if p.db != nil {
		if err := p.db.SaveBatchProxies(records); err != nil {
			return 0, err
		}
		_ = p.LoadFromDB()
	} else {
		p.mu.Lock()
		p.nodes = append(p.nodes, records...)
		p.mu.Unlock()
	}

	return len(records), nil
}

// DeleteNode removes a proxy node by ID
func (p *Pool) DeleteNode(id string) error {
	if p.db != nil {
		if err := p.db.DeleteProxyNode(id); err != nil {
			return err
		}
		return p.LoadFromDB()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	var remaining []*storage.ProxyNodeRecord
	for _, n := range p.nodes {
		if n.ID != id {
			remaining = append(remaining, n)
		}
	}
	p.nodes = remaining
	return nil
}

// DeleteGroup removes all proxy nodes belonging to a group
func (p *Pool) DeleteGroup(group string) error {
	if p.db != nil {
		if err := p.db.DeleteProxyGroup(group); err != nil {
			return err
		}
		return p.LoadFromDB()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	var remaining []*storage.ProxyNodeRecord
	for _, n := range p.nodes {
		if n.GroupName != group {
			remaining = append(remaining, n)
		}
	}
	p.nodes = remaining
	return nil
}

// ToggleGroup enables or disables all proxies in a group
func (p *Pool) ToggleGroup(group string, enable bool) error {
	if p.db != nil {
		if err := p.db.ToggleProxyGroup(group, enable); err != nil {
			return err
		}
		return p.LoadFromDB()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	for _, n := range p.nodes {
		if n.GroupName == group {
			n.IsActive = enable
		}
	}
	return nil
}

// ListNodes returns all registered proxy nodes
func (p *Pool) ListNodes() []*storage.ProxyNodeRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]*storage.ProxyNodeRecord, len(p.nodes))
	copy(result, p.nodes)
	return result
}

// ListNodesByGroup returns proxy nodes filtered by group name
func (p *Pool) ListNodesByGroup(group string) []*storage.ProxyNodeRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []*storage.ProxyNodeRecord
	for _, n := range p.nodes {
		if group == "" || n.GroupName == group {
			result = append(result, n)
		}
	}
	return result
}

// ListGroups returns distinct group names
func (p *Pool) ListGroups() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	seen := make(map[string]bool)
	var groups []string
	for _, n := range p.nodes {
		g := n.GroupName
		if g == "" {
			g = "default"
		}
		if !seen[g] {
			seen[g] = true
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		groups = []string{"default"}
	}
	return groups
}

// PickNext selects the next active proxy node across all groups
func (p *Pool) PickNext() (*storage.ProxyNodeRecord, *url.URL, error) {
	return p.PickNextForGroup("")
}

// PickNextForGroup selects next active proxy node specifically within a group
func (p *Pool) PickNextForGroup(group string) (*storage.ProxyNodeRecord, *url.URL, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.enabled || len(p.nodes) == 0 {
		return nil, nil, nil // Direct
	}

	var activeNodes []*storage.ProxyNodeRecord
	for _, n := range p.nodes {
		if n.IsActive {
			if group == "" || n.GroupName == group {
				activeNodes = append(activeNodes, n)
			}
		}
	}

	if len(activeNodes) == 0 {
		return nil, nil, nil // Fallback to direct
	}

	var chosen *storage.ProxyNodeRecord

	switch p.strategy {
	case "random":
		idx := rand.Intn(len(activeNodes))
		chosen = activeNodes[idx]

	case "least-errors":
		minFails := int(^uint(0) >> 1)
		for _, n := range activeNodes {
			if n.FailCount < minFails {
				minFails = n.FailCount
				chosen = n
			}
		}

	case "best-latency":
		minLatency := int(^uint(0) >> 1)
		for _, n := range activeNodes {
			if n.AvgLatencyMs > 0 && n.AvgLatencyMs < minLatency {
				minLatency = n.AvgLatencyMs
				chosen = n
			}
		}
		if chosen == nil {
			chosen = activeNodes[0]
		}

	default: // "round-robin"
		p.currentIndex = (p.currentIndex + 1) % len(activeNodes)
		chosen = activeNodes[p.currentIndex]
	}

	if chosen == nil {
		return nil, nil, nil
	}

	parsedURL, err := url.Parse(chosen.URL)
	if err != nil {
		return nil, nil, err
	}

	return chosen, parsedURL, nil
}

// RecordResult updates proxy node performance metrics
func (p *Pool) RecordResult(id string, success bool, latency time.Duration) {
	ms := int(latency.Milliseconds())
	if p.db != nil && id != "" {
		_ = p.db.UpdateProxyStats(id, success, ms)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	for _, n := range p.nodes {
		if n.ID == id {
			now := time.Now()
			n.LastChecked = &now
			if success {
				n.SuccessCount++
				if n.AvgLatencyMs == 0 {
					n.AvgLatencyMs = ms
				} else {
					n.AvgLatencyMs = (n.AvgLatencyMs*3 + ms) / 4
				}
			} else {
				n.FailCount++
			}
			break
		}
	}
}

// CheckHealth tests connectivity and latency of a single node
func (p *Pool) CheckHealth(ctx context.Context, id string) (int, error) {
	var target *storage.ProxyNodeRecord
	p.mu.RLock()
	for _, n := range p.nodes {
		if n.ID == id {
			target = n
			break
		}
	}
	p.mu.RUnlock()

	if target == nil {
		return 0, fmt.Errorf("proxy node tidak ditemukan")
	}

	parsedURL, err := url.Parse(target.URL)
	if err != nil {
		p.RecordResult(id, false, 0)
		return 0, err
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(parsedURL),
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 15 * time.Second,
		}).DialContext,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: false},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   8 * time.Second,
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://httpbin.org/ip", nil)
	if err != nil {
		req, _ = http.NewRequestWithContext(ctx, "GET", "https://api.openai.com/v1/models", nil)
	}

	resp, err := client.Do(req)
	duration := time.Since(start)
	if err != nil {
		p.RecordResult(id, false, duration)
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode != 401 && resp.StatusCode != 403 {
		p.RecordResult(id, false, duration)
		return int(duration.Milliseconds()), fmt.Errorf("status code %d", resp.StatusCode)
	}

	p.RecordResult(id, true, duration)
	return int(duration.Milliseconds()), nil
}

// CheckAllHealth runs health checks across all proxy nodes in parallel
func (p *Pool) CheckAllHealth(ctx context.Context) map[string]int {
	return p.CheckGroupHealth(ctx, "")
}

// CheckGroupHealth runs parallel health checks for nodes within a specific group
func (p *Pool) CheckGroupHealth(ctx context.Context, group string) map[string]int {
	nodes := p.ListNodesByGroup(group)
	results := make(map[string]int)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, n := range nodes {
		wg.Add(1)
		go func(node *storage.ProxyNodeRecord) {
			defer wg.Done()
			latency, err := p.CheckHealth(ctx, node.ID)
			mu.Lock()
			if err != nil {
				results[node.ID] = -1
			} else {
				results[node.ID] = latency
			}
			mu.Unlock()
		}(n)
	}

	wg.Wait()
	return results
}

// RoundTripper implements dynamic proxy failover for http.Client
type RoundTripper struct {
	pool              *Pool
	group             string
	fallbackTransport http.RoundTripper
	maxRetries        int
}

// NewTransport creates an http.RoundTripper that routes requests through all proxy nodes
func (p *Pool) NewTransport(fallback http.RoundTripper) http.RoundTripper {
	return p.NewTransportForGroup("", fallback)
}

// NewTransportForGroup creates an http.RoundTripper specifically for a proxy group
func (p *Pool) NewTransportForGroup(group string, fallback http.RoundTripper) http.RoundTripper {
	if fallback == nil {
		fallback = &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
		}
	}
	return &RoundTripper{
		pool:              p,
		group:             group,
		fallbackTransport: fallback,
		maxRetries:        2,
	}
}

// RoundTrip executes an HTTP request with automatic proxy rotation and failover
func (rt *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.pool == nil || !rt.pool.IsEnabled() {
		return rt.fallbackTransport.RoundTrip(req)
	}

	var lastErr error
	for attempt := 0; attempt <= rt.maxRetries; attempt++ {
		node, proxyURL, err := rt.pool.PickNextForGroup(rt.group)
		if err != nil || proxyURL == nil {
			// Direct fallback
			return rt.fallbackTransport.RoundTrip(req)
		}

		// Create dynamic transport using chosen proxy
		transport := &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			DialContext: (&net.Dialer{
				Timeout:   15 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			IdleConnTimeout:     90 * time.Second,
		}

		start := time.Now()
		resp, err := transport.RoundTrip(req)
		latency := time.Since(start)

		if err == nil && resp != nil {
			if node != nil {
				rt.pool.RecordResult(node.ID, true, latency)
			}
			return resp, nil
		}

		if node != nil {
			rt.pool.RecordResult(node.ID, false, latency)
		}
		lastErr = err
	}

	// Fallback to direct connection if all proxy attempts fail
	resp, err := rt.fallbackTransport.RoundTrip(req)
	if err != nil && lastErr != nil {
		return nil, fmt.Errorf("direct connection failed (%w); last proxy error: %v", err, lastErr)
	}
	return resp, err
}

// NewHTTPClient returns an http.Client pre-configured with global proxy pool failover transport
func (p *Pool) NewHTTPClient(timeout time.Duration) *http.Client {
	return p.NewHTTPClientForGroup("", timeout)
}

// NewHTTPClientForGroup returns an http.Client configured specifically for a proxy group
func (p *Pool) NewHTTPClientForGroup(group string, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: p.NewTransportForGroup(group, nil),
		Timeout:   timeout,
	}
}
