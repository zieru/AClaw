//go:build bifrostcache

package bifrost

import (
	"context"
	"fmt"
	"time"

	"goassistant/internal/config"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	vectorstore "github.com/maximhq/bifrost/framework/vectorstore"
	"github.com/maximhq/bifrost/plugins/semanticcache"
)

// buildPlugins assembles the Bifrost LLM plugins enabled in config. Currently
// this wires the semantic_cache plugin. Default mode is direct-only (exact-match,
// no embedding provider, dimension=1); semantic mode is possible by providing an
// embedding provider + dimension.
func buildPlugins(ctx context.Context, cfg config.BifrostConfig) ([]schemas.LLMPlugin, error) {
	var plugins []schemas.LLMPlugin

	if cfg.SemanticCache {
		if cfg.VectorStore != "redis" {
			return nil, fmt.Errorf("semantic_cache memerlukan vector_store 'redis'")
		}
		addr := cfg.VectorStoreAddr
		if addr == "" {
			addr = "127.0.0.1:6379"
		}
		logger := bifrost.NewDefaultLogger(schemas.LogLevelInfo)

		vs, err := vectorstore.NewVectorStore(ctx, &vectorstore.Config{
			Enabled: true,
			Type:    vectorstore.VectorStoreTypeRedis,
			Config: vectorstore.RedisConfig{
				Addr: &schemas.SecretVar{Val: addr},
			},
		}, logger)
		if err != nil {
			return nil, fmt.Errorf("semantic_cache vector store: %w", err)
		}

		ttl := 5 * time.Minute
		if cfg.SemanticCacheTTL != "" {
			if d, perr := time.ParseDuration(cfg.SemanticCacheTTL); perr == nil && d > 0 {
				ttl = d
			}
		}

		plugin, err := semanticcache.Init(ctx, &semanticcache.Config{
			Dimension:            1, // direct-only mode
			TTL:                  ttl,
			CacheByModel:         schemas.Ptr(true),
			CacheByProvider:      schemas.Ptr(true),
			VectorStoreNamespace: "GoAssistantSemanticCache",
		}, logger, vs)
		if err != nil {
			return nil, fmt.Errorf("semantic_cache plugin: %w", err)
		}
		plugins = append(plugins, plugin)
	}

	return plugins, nil
}

// setCacheKey scopes the request to a semantic-cache partition.
func setCacheKey(ctx *schemas.BifrostContext, key string) {
	if ctx == nil || key == "" {
		return
	}
	ctx.SetValue(semanticcache.CacheKey, key)
}
