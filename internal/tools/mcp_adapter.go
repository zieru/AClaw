package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// MCPToolWrapper wraps an MCP server tool to satisfy the goassistant tools.Tool interface
type MCPToolWrapper struct {
	client      *mcpclient.Client
	rawName     string
	toolName    string
	description string
	schema      ParametersSchema
}

func (w *MCPToolWrapper) Name() string {
	return w.toolName
}

func (w *MCPToolWrapper) Description() string {
	return w.description
}

func (w *MCPToolWrapper) Parameters() ParametersSchema {
	return w.schema
}

func (w *MCPToolWrapper) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = w.rawName
	req.Params.Arguments = args

	res, err := w.client.CallTool(ctx, req)
	if err != nil {
		return "", fmt.Errorf("gagal memanggil tool MCP '%s': %w", w.toolName, err)
	}

	output := formatCallToolResult(res)
	if res != nil && res.IsError {
		return output, fmt.Errorf("tool MCP '%s' mengembalikan error: %s", w.toolName, output)
	}
	return output, nil
}

func formatCallToolResult(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var sb strings.Builder
	for i, c := range res.Content {
		if i > 0 {
			sb.WriteString("\n")
		}
		switch v := c.(type) {
		case mcp.TextContent:
			sb.WriteString(v.Text)
		case *mcp.TextContent:
			sb.WriteString(v.Text)
		default:
			if b, err := json.Marshal(v); err == nil {
				sb.Write(b)
			} else {
				sb.WriteString(fmt.Sprintf("%v", v))
			}
		}
	}
	if sb.Len() == 0 && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			sb.Write(b)
		}
	}
	return sb.String()
}

func convertMCPSchema(schema mcp.ToolInputSchema) ParametersSchema {
	res := ParametersSchema{
		Type:       "object",
		Properties: make(map[string]ParameterProperty),
		Required:   schema.Required,
	}
	if schema.Type != "" {
		res.Type = schema.Type
	}
	for k, v := range schema.Properties {
		res.Properties[k] = parseParameterProperty(v)
	}
	return res
}

func parseParameterProperty(val any) ParameterProperty {
	prop := ParameterProperty{Type: "string"}
	switch v := val.(type) {
	case ParameterProperty:
		return v
	case map[string]any:
		if t, ok := v["type"].(string); ok && t != "" {
			prop.Type = t
		}
		if d, ok := v["description"].(string); ok {
			prop.Description = d
		}
		if enums, ok := v["enum"].([]any); ok {
			for _, e := range enums {
				if es, ok := e.(string); ok {
					prop.Enum = append(prop.Enum, es)
				}
			}
		} else if enums, ok := v["enum"].([]string); ok {
			prop.Enum = enums
		}
	default:
		if b, err := json.Marshal(val); err == nil {
			var p map[string]any
			if err := json.Unmarshal(b, &p); err == nil {
				if t, ok := p["type"].(string); ok && t != "" {
					prop.Type = t
				}
				if d, ok := p["description"].(string); ok {
					prop.Description = d
				}
				if enums, ok := p["enum"].([]any); ok {
					for _, e := range enums {
						if es, ok := e.(string); ok {
							prop.Enum = append(prop.Enum, es)
						}
					}
				}
			}
		}
	}
	return prop
}

// MCPManager manages MCP client connections and tool registration
type MCPManager struct {
	mu      sync.Mutex
	configs []config.MCPServerConfig
	clients []*mcpclient.Client
}

// NewMCPManager creates a new MCPManager
func NewMCPManager(configs []config.MCPServerConfig) *MCPManager {
	return &MCPManager{
		configs: configs,
	}
}

// StartAndRegister connects to all enabled MCP servers and registers their tools
func (m *MCPManager) StartAndRegister(ctx context.Context, reg *Registry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, srvCfg := range m.configs {
		if !srvCfg.Enabled {
			continue
		}

		log.Printf("🔌 [MCP] Menghubungkan ke server '%s' (Transport: %s)...", srvCfg.Name, srvCfg.Transport)

		var client *mcpclient.Client
		switch strings.ToLower(srvCfg.Transport) {
		case "sse":
			if srvCfg.URL == "" {
				log.Printf("⚠️ [MCP] URL kosong untuk SSE server '%s', dilewati", srvCfg.Name)
				continue
			}
			sseTrans, err := transport.NewSSE(srvCfg.URL)
			if err != nil {
				log.Printf("⚠️ [MCP] Gagal inisialisasi SSE transport '%s': %v", srvCfg.Name, err)
				continue
			}
			client = mcpclient.NewClient(sseTrans)
		case "stdio", "":
			if srvCfg.Command == "" {
				log.Printf("⚠️ [MCP] Command kosong untuk stdio server '%s', dilewati", srvCfg.Name)
				continue
			}
			envList := os.Environ()
			for k, v := range srvCfg.Env {
				envList = append(envList, fmt.Sprintf("%s=%s", k, v))
			}
			stdioTrans := transport.NewStdio(srvCfg.Command, envList, srvCfg.Args...)
			client = mcpclient.NewClient(stdioTrans)
		default:
			log.Printf("⚠️ [MCP] Transport tidak didukung '%s' untuk server '%s'", srvCfg.Transport, srvCfg.Name)
			continue
		}

		if err := client.Start(ctx); err != nil {
			log.Printf("⚠️ [MCP] Gagal memulai transport untuk server '%s': %v", srvCfg.Name, err)
			_ = client.Close()
			continue
		}

		initCtx, cancelInit := context.WithTimeout(ctx, 15*time.Second)
		initReq := mcp.InitializeRequest{}
		initReq.Params.ClientInfo = mcp.Implementation{
			Name:    "goassistant",
			Version: "1.0.0",
		}
		initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION

		_, err := client.Initialize(initCtx, initReq)
		cancelInit()
		if err != nil {
			log.Printf("⚠️ [MCP] Handshake/Initialize gagal untuk server '%s': %v", srvCfg.Name, err)
			_ = client.Close()
			continue
		}

		listCtx, cancelList := context.WithTimeout(ctx, 10*time.Second)
		listReq := mcp.ListToolsRequest{}
		listRes, err := client.ListTools(listCtx, listReq)
		cancelList()
		if err != nil {
			log.Printf("⚠️ [MCP] Gagal mendapatkan daftar tool dari server '%s': %v", srvCfg.Name, err)
			_ = client.Close()
			continue
		}

		registeredCount := 0
		for _, t := range listRes.Tools {
			toolName := t.Name
			if srvCfg.Prefix != "" {
				toolName = srvCfg.Prefix + "_" + t.Name
			}

			wrapper := &MCPToolWrapper{
				client:      client,
				rawName:     t.Name,
				toolName:    toolName,
				description: t.Description,
				schema:      convertMCPSchema(t.InputSchema),
			}

			reg.Register(wrapper)
			registeredCount++
		}

		m.clients = append(m.clients, client)
		log.Printf("✅ [MCP] Server '%s' berhasil terhubung: %d tool(s) didaftarkan (Prefix: '%s')",
			srvCfg.Name, registeredCount, srvCfg.Prefix)
	}

	return nil
}

// Close gracefully closes all active MCP client transports
func (m *MCPManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, c := range m.clients {
		if c != nil {
			_ = c.Close()
		}
	}
	m.clients = nil
	return nil
}
