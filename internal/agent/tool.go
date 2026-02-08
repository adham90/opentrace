package agent

import "context"

// ToolParam describes a parameter for a Tool.
type ToolParam struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "string", "int", "bool"
	Required bool   `json:"required"`
}

// Tool represents an agent tool exposed by a connector.
type Tool struct {
	Name        string
	Description string
	Params      []ToolParam
	Handler     func(ctx context.Context, args map[string]any) (string, error)
}
