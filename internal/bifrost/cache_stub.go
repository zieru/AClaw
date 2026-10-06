//go:build !bifrostcache

package bifrost

import (
	"context"

	"goassistant/internal/config"

	"github.com/maximhq/bifrost/core/schemas"
)

// buildPlugins is a no-op when the binary is built without the `bifrostcache`
// tag (keeps the default binary lean; semantic caching needs Redis).
func buildPlugins(ctx context.Context, cfg config.BifrostConfig) ([]schemas.LLMPlugin, error) {
	return nil, nil
}

// setCacheKey is a no-op without the semantic cache plugin.
func setCacheKey(ctx *schemas.BifrostContext, key string) {}
