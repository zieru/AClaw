package bifrost

import (
	"context"
	"errors"
	"strings"

	"goassistant/internal/config"
	"goassistant/internal/provider"
	"goassistant/internal/storage"
)

// GatewayProvider is a display-only provider that represents the embedded Bifrost
// gateway in the legacy provider manager. Routing is actually handled by the
// Router (Client), but keeping a registered provider lets the orchestrator, model
// UI and status dashboards resolve a default provider/model without crashing.
type GatewayProvider struct {
	client       *Client
	name         string
	providerType string
	defaultModel string
	models       []string
}

// NewGatewayProvider creates a display/routing facade for a DB provider record.
// All calls still go through Bifrost; this facade preserves GoAssistant's
// provider-name resolution used by policy overrides and Telegram commands.
func NewGatewayProvider(c *Client, rec *storage.ProviderRecord) *GatewayProvider {
	g := &GatewayProvider{client: c}
	if rec != nil {
		g.name = strings.TrimSpace(rec.Name)
		g.providerType = rec.Type
		g.defaultModel = rec.DefaultModel
		g.models = rec.EnabledModels()
	}
	if g.name == "" {
		g.name = "bifrost"
	}
	return g
}

func (g *GatewayProvider) Name() string { return g.name }

func (g *GatewayProvider) Type() string { return g.providerType }

func (g *GatewayProvider) DefaultModel() string {
	if c := config.Get(); c != nil && c.Defaults.DefaultModel != "" {
		return c.Defaults.DefaultModel
	}
	if g.defaultModel != "" {
		return g.defaultModel
	}
	if len(g.models) > 0 {
		return g.models[0]
	}
	return ""
}

func (g *GatewayProvider) Models() []string { return g.models }

func (g *GatewayProvider) SetHTTPClient(interface{}) {}

// GenerateChat routes a request through the Bifrost gateway.
func (g *GatewayProvider) GenerateChat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	if g.client == nil || !g.client.Enabled() {
		return nil, errors.New("bifrost gateway tidak aktif")
	}
	return g.client.Generate(ctx, g.name, req)
}
