package bifrost

import (
	"encoding/json"
	"strings"

	"goassistant/internal/provider"
	"goassistant/internal/tools"

	"github.com/maximhq/bifrost/core/schemas"
)

// mapRole maps a GoAssistant message role to a Bifrost chat message role.
func mapRole(r provider.MessageRole) schemas.ChatMessageRole {
	switch r {
	case provider.RoleSystem:
		return schemas.ChatMessageRoleSystem
	case provider.RoleAssistant:
		return schemas.ChatMessageRoleAssistant
	case provider.RoleTool:
		return schemas.ChatMessageRoleTool
	default:
		return schemas.ChatMessageRoleUser
	}
}

// mapRoleBack maps a Bifrost chat message role back to GoAssistant.
func mapRoleBack(r schemas.ChatMessageRole) provider.MessageRole {
	switch r {
	case schemas.ChatMessageRoleSystem:
		return provider.RoleSystem
	case schemas.ChatMessageRoleAssistant:
		return provider.RoleAssistant
	case schemas.ChatMessageRoleTool:
		return provider.RoleTool
	default:
		return provider.RoleUser
	}
}

// toBifrostMessages converts GoAssistant chat messages to Bifrost chat messages.
func toBifrostMessages(msgs []provider.ChatMessage) []schemas.ChatMessage {
	out := make([]schemas.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		bm := schemas.ChatMessage{Role: mapRole(m.Role)}

		if len(m.Images) > 0 {
			blocks := make([]schemas.ChatContentBlock, 0, 1+len(m.Images))
			if m.Content != "" {
				c := m.Content
				blocks = append(blocks, schemas.ChatContentBlock{Type: schemas.ChatContentBlockTypeText, Text: &c})
			}
			for _, img := range m.Images {
				blocks = append(blocks, schemas.ChatContentBlock{
					Type:           schemas.ChatContentBlockTypeImage,
					ImageURLStruct: &schemas.ChatInputImage{URL: img},
				})
			}
			bm.Content = &schemas.ChatMessageContent{ContentBlocks: blocks}
		} else {
			c := m.Content
			bm.Content = &schemas.ChatMessageContent{ContentStr: &c}
		}

		if m.ToolCallID != "" {
			id := m.ToolCallID
			bm.ChatToolMessage = &schemas.ChatToolMessage{ToolCallID: &id}
		}

		if len(m.ToolCalls) > 0 {
			tcs := make([]schemas.ChatAssistantMessageToolCall, 0, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				var argJSON string
				if tc.Arguments != nil {
					b, err := json.Marshal(tc.Arguments)
					if err == nil {
						argJSON = string(b)
					}
				}
				id, name := tc.ID, tc.Name
				tcs = append(tcs, schemas.ChatAssistantMessageToolCall{
					Index: uint16(i),
					ID:    &id,
					Type:  schemas.Ptr("function"),
					Function: schemas.ChatAssistantMessageToolCallFunction{
						Name:      &name,
						Arguments: argJSON,
					},
				})
			}
			bm.ChatAssistantMessage = &schemas.ChatAssistantMessage{ToolCalls: tcs}
		}

		out = append(out, bm)
	}
	return out
}

// fromBifrostMessage converts a Bifrost chat message back to GoAssistant.
func fromBifrostMessage(m schemas.ChatMessage) provider.ChatMessage {
	pm := provider.ChatMessage{Role: mapRoleBack(m.Role)}
	if m.Content != nil {
		if m.Content.ContentStr != nil {
			pm.Content = *m.Content.ContentStr
		} else {
			var sb strings.Builder
			for _, blk := range m.Content.ContentBlocks {
				if blk.Text != nil {
					sb.WriteString(*blk.Text)
				}
			}
			pm.Content = sb.String()
		}
	}
	if m.ChatAssistantMessage != nil && len(m.ChatAssistantMessage.ToolCalls) > 0 {
		for _, tc := range m.ChatAssistantMessage.ToolCalls {
			var args map[string]interface{}
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			id := ""
			name := ""
			if tc.ID != nil {
				id = *tc.ID
			}
			if tc.Function.Name != nil {
				name = *tc.Function.Name
			}
			pm.ToolCalls = append(pm.ToolCalls, provider.ToolCall{ID: id, Name: name, Arguments: args})
		}
	}
	if m.ChatToolMessage != nil && m.ChatToolMessage.ToolCallID != nil {
		pm.ToolCallID = *m.ChatToolMessage.ToolCallID
	}
	return pm
}

// thinkingFromMessage extracts reasoning text from a Bifrost assistant message.
func thinkingFromMessage(m *schemas.ChatMessage) string {
	if m == nil || m.ChatAssistantMessage == nil {
		return ""
	}
	if m.ChatAssistantMessage.Reasoning != nil {
		return *m.ChatAssistantMessage.Reasoning
	}
	var sb strings.Builder
	for _, d := range m.ChatAssistantMessage.ReasoningDetails {
		if d.Text != nil {
			sb.WriteString(*d.Text)
		} else if d.Summary != nil {
			sb.WriteString(*d.Summary)
		}
	}
	return sb.String()
}

// toBifrostTools converts GoAssistant tools to Bifrost function tools.
func toBifrostTools(tools []tools.Tool) []schemas.ChatTool {
	out := make([]schemas.ChatTool, 0, len(tools))
	for _, t := range tools {
		ps := t.Parameters()
		props := schemas.NewOrderedMapWithCapacity(len(ps.Properties))
		for k, v := range ps.Properties {
			prop := schemas.NewOrderedMapFromPairs(
				schemas.KV("type", v.Type),
				schemas.KV("description", v.Description),
			)
			if len(v.Enum) > 0 {
				prop.Set("enum", v.Enum)
			}
			props.Set(k, prop)
		}
		params := &schemas.ToolFunctionParameters{
			Type:       "object",
			Properties: props,
			Required:   ps.Required,
		}
		desc := t.Description()
		out = append(out, schemas.ChatTool{
			Type: schemas.ChatToolTypeFunction,
			Function: &schemas.ChatToolFunction{
				Name:        t.Name(),
				Description: &desc,
				Parameters:  params,
			},
		})
	}
	return out
}

// mergeToolCallDelta accumulates a streaming tool-call delta into the response.
// rawArgs tracks unmarshaled argument JSON fragments per tool call index.
func mergeToolCallDelta(out *provider.ChatResponse, delta *schemas.ChatStreamResponseChoiceDelta, rawArgs *map[int]*strings.Builder) {
	if delta == nil || len(delta.ToolCalls) == 0 {
		return
	}
	if *rawArgs == nil {
		*rawArgs = make(map[int]*strings.Builder)
	}
	for _, tc := range delta.ToolCalls {
		idx := int(tc.Index)
		if out.ToolCalls == nil {
			out.ToolCalls = make([]provider.ToolCall, 0)
		}
		for len(out.ToolCalls) <= idx {
			out.ToolCalls = append(out.ToolCalls, provider.ToolCall{
				Arguments: make(map[string]interface{}),
			})
		}
		if tc.ID != nil {
			out.ToolCalls[idx].ID += *tc.ID
		}
		if tc.Function.Name != nil {
			out.ToolCalls[idx].Name += *tc.Function.Name
		}
		if tc.Function.Arguments != "" {
			sb, ok := (*rawArgs)[idx]
			if !ok {
				sb = &strings.Builder{}
				(*rawArgs)[idx] = sb
			}
			sb.WriteString(tc.Function.Arguments)
		}
	}
}

// finalizeToolCalls parses accumulated JSON arguments for all tool calls.
func finalizeToolCalls(out *provider.ChatResponse, rawArgs map[int]*strings.Builder) {
	if out == nil || len(out.ToolCalls) == 0 {
		return
	}
	for idx := range out.ToolCalls {
		if out.ToolCalls[idx].Arguments == nil {
			out.ToolCalls[idx].Arguments = make(map[string]interface{})
		}
		if sb, ok := rawArgs[idx]; ok && sb.Len() > 0 {
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(sb.String()), &parsed); err == nil && parsed != nil {
				out.ToolCalls[idx].Arguments = parsed
			}
		}
	}
}

