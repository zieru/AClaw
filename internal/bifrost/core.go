package bifrost

import (
	"context"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

// schemasBifrost is a readable alias for the Bifrost core type.
type schemasBifrost = bifrost.Bifrost

// initCore initializes a Bifrost core instance from config.
func initCore(ctx context.Context, cfg schemas.BifrostConfig) (*bifrost.Bifrost, error) {
	return bifrost.Init(ctx, cfg)
}
