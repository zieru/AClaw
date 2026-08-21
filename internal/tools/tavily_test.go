package tools

import (
	"context"
	"testing"
)

func TestTavilySearchToolSchema(t *testing.T) {
	tool := &TavilySearchTool{}
	if tool.Name() != "tavily_search" {
		t.Errorf("expected name tavily_search, got %s", tool.Name())
	}

	params := tool.Parameters()
	if params.Type != "object" {
		t.Errorf("expected object type schema")
	}
	if _, ok := params.Properties["query"]; !ok {
		t.Errorf("expected query property in schema")
	}
}

func TestTavilySearchWithoutKey(t *testing.T) {
	tool := &TavilySearchTool{}
	// Calling without API Key should return clear error
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"query": "golang tutorial",
	})
	if err == nil {
		t.Errorf("expected error when API key is missing")
	}
}

func TestRegistryHasTavily(t *testing.T) {
	reg := GetRegistry()
	tool, ok := reg.Get("tavily_search")
	if !ok || tool == nil {
		t.Fatalf("expected tavily_search to be registered in default registry")
	}
}
