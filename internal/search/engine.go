package search

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
)

// Engine coordinates multi-provider web search execution with intelligent failover
type Engine struct {
	cfg       config.SearchConfig
	providers map[string]Provider
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
		// Auto prioritization: Tavily -> Firecrawl -> DuckDuckGo
		addProvider("tavily")
		addProvider("firecrawl")
		addProvider("duckduckgo")
	}

	return plan
}
