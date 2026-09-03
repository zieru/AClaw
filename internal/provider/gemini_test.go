package provider

import (
	"testing"
)

func TestBuildGeminiContents(t *testing.T) {
	messages := []ChatMessage{
		{
			Role:    RoleSystem,
			Content: "You are a helpful assistant.",
		},
		{
			Role:    RoleUser,
			Content: "What is the weather?",
		},
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{
					ID:   "call_1",
					Name: "get_weather",
					Arguments: map[string]interface{}{
						"location": "Jakarta",
					},
				},
				{
					ID:   "call_2",
					Name: "get_time",
					Arguments: map[string]interface{}{
						"timezone": "WIB",
					},
				},
			},
		},
		{
			Role:    RoleTool,
			Name:    "get_weather",
			Content: `{"temp": 30, "condition": "sunny"}`,
		},
		{
			Role:    RoleTool,
			Name:    "get_time",
			Content: "21:00 WIB",
		},
	}

	contents, systemContent := buildGeminiContents(messages)

	// Check system content
	if systemContent == nil || len(systemContent.Parts) == 0 {
		t.Fatalf("expected systemContent with parts, got nil or empty")
	}
	if systemContent.Parts[0].Text != "You are a helpful assistant." {
		t.Errorf("unexpected system content: %v", systemContent.Parts[0].Text)
	}

	// Verify conversation turns
	// Turn 0: User ("What is the weather?")
	// Turn 1: Model (2 function calls)
	// Turn 2: User (2 function responses merged into 1 user turn)
	if len(contents) != 3 {
		t.Fatalf("expected 3 merged contents turns, got %d", len(contents))
	}

	if contents[0].Role != "user" {
		t.Errorf("turn 0 role: expected 'user', got '%s'", contents[0].Role)
	}

	if contents[1].Role != "model" {
		t.Errorf("turn 1 role: expected 'model', got '%s'", contents[1].Role)
	}
	if len(contents[1].Parts) != 2 {
		t.Errorf("turn 1 parts: expected 2 functionCall parts, got %d", len(contents[1].Parts))
	}

	// Crucial test: Turn 2 must be role "user", NEVER "function"
	if contents[2].Role != "user" {
		t.Errorf("turn 2 role: expected 'user', got '%s'", contents[2].Role)
	}

	// Must have 2 merged functionResponse parts
	if len(contents[2].Parts) != 2 {
		t.Fatalf("turn 2 parts: expected 2 functionResponse parts, got %d", len(contents[2].Parts))
	}

	// Verify Part 1 (JSON unmarshaled)
	part1 := contents[2].Parts[0]
	if part1.FunctionResponse == nil {
		t.Fatalf("part 1: expected FunctionResponse, got nil")
	}
	if part1.Text != "" {
		t.Errorf("part 1: text should be empty, got '%s'", part1.Text)
	}
	if part1.FunctionResponse.Name != "get_weather" {
		t.Errorf("part 1 name: expected 'get_weather', got '%s'", part1.FunctionResponse.Name)
	}
	if part1.FunctionResponse.Response["condition"] != "sunny" {
		t.Errorf("part 1 response: expected condition sunny, got %v", part1.FunctionResponse.Response)
	}

	// Verify Part 2 (plain text fallback wrapped in output)
	part2 := contents[2].Parts[1]
	if part2.FunctionResponse == nil {
		t.Fatalf("part 2: expected FunctionResponse, got nil")
	}
	if part2.Text != "" {
		t.Errorf("part 2: text should be empty, got '%s'", part2.Text)
	}
	if part2.FunctionResponse.Name != "get_time" {
		t.Errorf("part 2 name: expected 'get_time', got '%s'", part2.FunctionResponse.Name)
	}
	if part2.FunctionResponse.Response["output"] != "21:00 WIB" {
		t.Errorf("part 2 response: expected output '21:00 WIB', got %v", part2.FunctionResponse.Response)
	}
}
