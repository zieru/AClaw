package bifrost

import (
	"strings"
	"testing"

	"goassistant/internal/provider"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestMergeToolCallDelta_StreamingAggregation(t *testing.T) {
	out := &provider.ChatResponse{}
	var rawArgs map[int]*strings.Builder

	// Chunk 1: tool ID and partial name
	namePart1 := "bash_"
	id1 := "call_123"
	d1 := &schemas.ChatStreamResponseChoiceDelta{
		ToolCalls: []schemas.ChatAssistantMessageToolCall{
			{
				Index: 0,
				ID:    &id1,
				Function: schemas.ChatAssistantMessageToolCallFunction{
					Name:      &namePart1,
					Arguments: `{"command": "up`,
				},
			},
		},
	}
	mergeToolCallDelta(out, d1, &rawArgs)

	// Chunk 2: rest of name and middle arguments
	namePart2 := "exec"
	d2 := &schemas.ChatStreamResponseChoiceDelta{
		ToolCalls: []schemas.ChatAssistantMessageToolCall{
			{
				Index: 0,
				Function: schemas.ChatAssistantMessageToolCallFunction{
					Name:      &namePart2,
					Arguments: `time && free -m`,
				},
			},
		},
	}
	mergeToolCallDelta(out, d2, &rawArgs)

	// Chunk 3: end of arguments
	d3 := &schemas.ChatStreamResponseChoiceDelta{
		ToolCalls: []schemas.ChatAssistantMessageToolCall{
			{
				Index: 0,
				Function: schemas.ChatAssistantMessageToolCallFunction{
					Arguments: `"}`,
				},
			},
		},
	}
	mergeToolCallDelta(out, d3, &rawArgs)

	// Finalize tool calls
	finalizeToolCalls(out, rawArgs)

	if len(out.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(out.ToolCalls))
	}
	tc := out.ToolCalls[0]
	if tc.ID != "call_123" {
		t.Errorf("expected ID call_123, got %q", tc.ID)
	}
	if tc.Name != "bash_exec" {
		t.Errorf("expected Name bash_exec, got %q", tc.Name)
	}
	cmd, ok := tc.Arguments["command"].(string)
	if !ok || cmd != "uptime && free -m" {
		t.Errorf("expected command 'uptime && free -m', got %v (args: %#v)", cmd, tc.Arguments)
	}
}
