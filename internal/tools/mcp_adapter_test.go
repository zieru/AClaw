package tools

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"goassistant/internal/config"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestConvertMCPSchema(t *testing.T) {
	inputSchema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]any{
			"dataset": map[string]any{
				"type":        "string",
				"description": "Dataset name or file path",
			},
			"limit": map[string]any{
				"type":        "number",
				"description": "Maximum rows",
			},
			"format": map[string]any{
				"type":        "string",
				"description": "Output format",
				"enum":        []any{"json", "table", "csv"},
			},
		},
		Required: []string{"dataset"},
	}

	converted := convertMCPSchema(inputSchema)

	if converted.Type != "object" {
		t.Fatalf("expected type 'object', got %s", converted.Type)
	}
	if len(converted.Required) != 1 || converted.Required[0] != "dataset" {
		t.Fatalf("unexpected required: %v", converted.Required)
	}

	datasetProp, ok := converted.Properties["dataset"]
	if !ok || datasetProp.Type != "string" || datasetProp.Description != "Dataset name or file path" {
		t.Fatalf("unexpected datasetProp: %+v", datasetProp)
	}

	limitProp, ok := converted.Properties["limit"]
	if !ok || limitProp.Type != "number" || limitProp.Description != "Maximum rows" {
		t.Fatalf("unexpected limitProp: %+v", limitProp)
	}

	formatProp, ok := converted.Properties["format"]
	if !ok || len(formatProp.Enum) != 3 || formatProp.Enum[0] != "json" {
		t.Fatalf("unexpected formatProp: %+v", formatProp)
	}
}

func TestFormatCallToolResult(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: "sample output",
			},
		},
	}

	formatted := formatCallToolResult(res)
	if formatted != "sample output" {
		t.Fatalf("expected 'sample output', got %q", formatted)
	}
}

func TestMCPToolWrapperInterface(t *testing.T) {
	wrapper := &MCPToolWrapper{
		rawName:     "query_analytics",
		toolName:    "a7g3_query_analytics",
		description: "Query analytics DuckDB engine",
		schema: ParametersSchema{
			Type: "object",
			Properties: map[string]ParameterProperty{
				"dataset": {Type: "string", Description: "Dataset path"},
			},
			Required: []string{"dataset"},
		},
	}

	var tool Tool = wrapper
	if tool.Name() != "a7g3_query_analytics" {
		t.Errorf("expected a7g3_query_analytics, got %s", tool.Name())
	}
	if tool.Description() != "Query analytics DuckDB engine" {
		t.Errorf("unexpected description: %s", tool.Description())
	}
	if len(tool.Parameters().Required) != 1 {
		t.Errorf("expected 1 required param, got %d", len(tool.Parameters().Required))
	}
}

func TestLiveA7G3Connection(t *testing.T) {
	cmdPath := "c:/Users/Grapari_Infomedia/GolandProjects/a7g3/g3a.exe"
	if _, err := os.Stat(cmdPath); err != nil {
		t.Skipf("g3a.exe not found at %s, skipping live test", cmdPath)
	}

	mgr := NewMCPManager([]config.MCPServerConfig{
		{
			Name:      "a7g3",
			Enabled:   true,
			Transport: "stdio",
			Command:   cmdPath,
			Args:      []string{"mcp"},
			Prefix:    "a7g3",
		},
	})
	defer mgr.Close()

	reg := &Registry{
		tools: make(map[string]Tool),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mgr.StartAndRegister(ctx, reg); err != nil {
		t.Fatalf("failed to start and register: %v", err)
	}

	expectedTools := []string{
		"a7g3_list_datasets",
		"a7g3_describe_dataset",
		"a7g3_query_analytics",
		"a7g3_run_sql",
	}

	for _, expected := range expectedTools {
		tool, ok := reg.Get(expected)
		if !ok {
			t.Errorf("expected tool %s to be registered", expected)
			continue
		}
		if tool.Name() != expected {
			t.Errorf("expected name %s, got %s", expected, tool.Name())
		}
	}

	// Test executing a7g3_run_sql
	sqlTool, ok := reg.Get("a7g3_run_sql")
	if !ok {
		t.Fatalf("a7g3_run_sql not found")
	}

	sqlRes, err := sqlTool.Execute(ctx, map[string]interface{}{
		"sql": "SELECT 42 AS answer, 'DuckDB via a7g3 MCP' AS message",
	})
	if err != nil {
		t.Fatalf("execute a7g3_run_sql failed: %v", err)
	}
	if !strings.Contains(sqlRes, "42") || !strings.Contains(sqlRes, "DuckDB") {
		t.Errorf("expected sql result to contain '42' and 'DuckDB', got: %s", sqlRes)
	}
	t.Logf("a7g3_run_sql live response:\n%s", sqlRes)
}


