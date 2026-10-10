package search

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"goassistant/internal/config"
)

// Engine coordinates multi-provider web search execution with intelligent failover and load balancing
type Engine struct {
	cfg       config.SearchConfig
	providers map[string]Provider
	rrCounter uint64
	mu        sync.RWMutex
}

var (
	globalEngine *Engine
	engineOnce   sync.Once
)

// InitGlobalEngine initializes the singleton search engine from configuration
func InitGlobalEngine(cfg config.SearchConfig) *Engine {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if cfg.Strategy == "" {
		cfg.Strategy = "fallback"
	}
	if cfg.Provider == "" {
		cfg.Provider = "auto"
	}

	eng := &Engine{
		cfg:       cfg,
		providers: make(map[string]Provider),
	}

	// Register core providers
	eng.RegisterProvider(NewTavilyProvider(cfg.Tavily, timeout))
	eng.RegisterProvider(NewFirecrawlProvider(cfg.Firecrawl, timeout))
	eng.RegisterProvider(NewDuckDuckGoProvider(timeout))

	globalEngine = eng
	return eng
}

// GetGlobalEngine returns the initialized singleton search engine
func GetGlobalEngine() *Engine {
	engineOnce.Do(func() {
		if globalEngine == nil {
			// Initialize with defaults if not previously initialized
			cfg := config.SearchConfig{
				Enabled:         true,
				Provider:        "auto",
				Strategy:        "fallback",
				MaxResults:      5,
				FallbackEnabled: true,
				TimeoutSeconds:  15,
				Tavily: config.TavilyConfig{
					BaseURL:       "https://api.tavily.com",
					SearchDepth:   "basic",
					IncludeAnswer: true,
				},
				Firecrawl: config.FirecrawlConfig{
					BaseURL: "https://api.firecrawl.dev",
				},
			}
			InitGlobalEngine(cfg)
		}
	})
	return globalEngine
}

// RegisterProvider registers or overrides a search provider
func (e *Engine) RegisterProvider(p Provider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.providers[p.Name()] = p
}

// GetProvider retrieves a provider by name
func (e *Engine) GetProvider(name string) (Provider, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.providers[strings.ToLower(name)]
	return p, ok
}

// AvailableProviders returns names of providers currently configured with valid keys
func (e *Engine) AvailableProviders() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var list []string
	for name, p := range e.providers {
		if p.IsAvailable() {
			list = append(list, name)
		}
	}
	return list
}

// ActiveProvider returns the primary provider configured
func (e *Engine) ActiveProvider() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.Provider
}

// Strategy returns the current strategy: "fallback" or "roundrobin"
func (e *Engine) Strategy() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cfg.Strategy == "" {
		return "fallback"
	}
	return e.cfg.Strategy
}

// SetStrategy updates the selection strategy dynamically
func (e *Engine) SetStrategy(strategy string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := strings.ToLower(strings.TrimSpace(strategy))
	if s == "roundrobin" || s == "round_robin" || s == "rr" {
		e.cfg.Strategy = "roundrobin"
	} else {
		e.cfg.Strategy = "fallback"
	}
}

// SetProvider updates active provider selection ("auto", "tavily", "firecrawl", "duckduckgo")
func (e *Engine) SetProvider(provider string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg.Provider = strings.ToLower(strings.TrimSpace(provider))
}

// SetFallback updates fallback enabled status
func (e *Engine) SetFallback(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg.FallbackEnabled = enabled
}

// SetTavilyKey updates Tavily API key and recreates the provider
func (e *Engine) SetTavilyKey(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg.Tavily.APIKey = strings.TrimSpace(key)
	timeout := time.Duration(e.cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	e.providers["tavily"] = NewTavilyProvider(e.cfg.Tavily, timeout)
}

// SetFirecrawlKey updates Firecrawl API key and recreates the provider
func (e *Engine) SetFirecrawlKey(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg.Firecrawl.APIKey = strings.TrimSpace(key)
	timeout := time.Duration(e.cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	e.providers["firecrawl"] = NewFirecrawlProvider(e.cfg.Firecrawl, timeout)
}

// SetTavilyDepth updates Tavily search depth ("basic" or "advanced")
func (e *Engine) SetTavilyDepth(depth string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := strings.ToLower(strings.TrimSpace(depth))
	if d == "advanced" {
		e.cfg.Tavily.SearchDepth = "advanced"
	} else {
		e.cfg.Tavily.SearchDepth = "basic"
	}
	timeout := time.Duration(e.cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	e.providers["tavily"] = NewTavilyProvider(e.cfg.Tavily, timeout)
}

// SetTavilyIncludeAnswer updates whether Tavily includes quick AI answer
func (e *Engine) SetTavilyIncludeAnswer(include bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg.Tavily.IncludeAnswer = include
	timeout := time.Duration(e.cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	e.providers["tavily"] = NewTavilyProvider(e.cfg.Tavily, timeout)
}

// SetFirecrawlBaseURL updates Firecrawl Base URL (e.g. self-hosted)
func (e *Engine) SetFirecrawlBaseURL(baseURL string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if b == "" {
		b = "https://api.firecrawl.dev"
	}
	e.cfg.Firecrawl.BaseURL = b
	timeout := time.Duration(e.cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	e.providers["firecrawl"] = NewFirecrawlProvider(e.cfg.Firecrawl, timeout)
}

// SetMaxResults updates default max search results per query
func (e *Engine) SetMaxResults(limit int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if limit <= 0 {
		limit = 5
	}
	e.cfg.MaxResults = limit
}

// UpdateConfig updates the full search configuration dynamically
func (e *Engine) UpdateConfig(cfg config.SearchConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg = cfg
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	e.providers["tavily"] = NewTavilyProvider(cfg.Tavily, timeout)
	e.providers["firecrawl"] = NewFirecrawlProvider(cfg.Firecrawl, timeout)
	e.providers["duckduckgo"] = NewDuckDuckGoProvider(timeout)
}

// Config returns a copy of current search configuration
func (e *Engine) Config() config.SearchConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg
}

// Search executes a web search query across providers based on configured strategy and failover
func (e *Engine) Search(ctx context.Context, query string) (*Response, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.cfg.Enabled {
		return nil, fmt.Errorf("web search engine dinonaktifkan di konfigurasi")
	}

	limit := e.cfg.MaxResults
	if limit <= 0 {
		limit = 5
	}

	plan := e.buildProviderPlan()
	if len(plan) == 0 {
		return nil, fmt.Errorf("tidak ada provider pencarian yang tersedia")
	}

	var lastErr error
	for _, p := range plan {
		res, err := p.Search(ctx, query, limit)
		if err == nil && res != nil && (len(res.Results) > 0 || res.Answer != "") {
			return res, nil
		}

		if err != nil {
			lastErr = err
			log.Printf("⚠️ [Search Engine] Provider %s gagal untuk query %q: %v", p.Name(), query, err)
		}

		// If fallback is disabled and the first provider was explicitly chosen, stop
		if !e.cfg.FallbackEnabled && strings.ToLower(e.cfg.Provider) != "auto" {
			break
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("semua provider pencarian gagal, error terakhir: %w", lastErr)
	}

	return nil, fmt.Errorf("pencarian web tidak menghasilkan informasi apapun untuk query %q", query)
}

// buildProviderPlan generates the ordered execution sequence of providers
func (e *Engine) buildProviderPlan() []Provider {
	providerName := strings.ToLower(strings.TrimSpace(e.cfg.Provider))
	if providerName == "" {
		providerName = "auto"
	}

	var plan []Provider
	visited := make(map[string]bool)

	addProvider := func(name string) {
		if visited[name] {
			return
		}
		if p, ok := e.providers[name]; ok && p.IsAvailable() {
			plan = append(plan, p)
			visited[name] = true
		}
	}

	switch providerName {
	case "tavily":
		addProvider("tavily")
		if e.cfg.FallbackEnabled {
			addProvider("firecrawl")
			addProvider("duckduckgo")
		}
	case "firecrawl":
		addProvider("firecrawl")
		if e.cfg.FallbackEnabled {
			addProvider("tavily")
			addProvider("duckduckgo")
		}
	case "duckduckgo":
		addProvider("duckduckgo")
	case "auto":
		fallthrough
	default:
		// Check strategy: Round-Robin vs Fallback (priority)
		if strings.ToLower(e.cfg.Strategy) == "roundrobin" {
			// Find available primary providers
			var availPrimaries []string
			for _, name := range []string{"tavily", "firecrawl"} {
				if p, ok := e.providers[name]; ok && p.IsAvailable() {
					availPrimaries = append(availPrimaries, name)
				}
			}

			if len(availPrimaries) > 1 {
				// Rotate start index atomically
				idx := int(atomic.AddUint64(&e.rrCounter, 1) % uint64(len(availPrimaries)))
				for i := 0; i < len(availPrimaries); i++ {
					currName := availPrimaries[(idx+i)%len(availPrimaries)]
					addProvider(currName)
				}
				if e.cfg.FallbackEnabled {
					addProvider("duckduckgo")
				}
				break
			}
		}

		// Fallback strategy: Priority Tavily -> Firecrawl -> DuckDuckGo
		addProvider("tavily")
		addProvider("firecrawl")
		addProvider("duckduckgo")
	}

	return plan
}
