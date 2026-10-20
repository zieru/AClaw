package tools

import (
	"context"
)

// ParameterProperty describes a single parameter property
type ParameterProperty struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

// ParametersSchema defines the JSON schema for tool arguments
type ParametersSchema struct {
	Type       string                        `json:"type"`
	Properties map[string]ParameterProperty `json:"properties"`
	Required   []string                      `json:"required,omitempty"`
}

// Tool defines an executable capability exposed to LLMs
type Tool interface {
	Name() string
	Description() string
	Parameters() ParametersSchema
	Execute(ctx context.Context, args map[string]interface{}) (string, error)
}
