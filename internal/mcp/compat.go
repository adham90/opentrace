package mcp

// compat.go provides helper functions that bridge common patterns from the
// old mark3labs/mcp-go SDK to the new modelcontextprotocol/go-sdk. This makes
// the migration largely mechanical across the 30+ tool files.
//
// NOTE: The shared helpers (NewToolResultText, NewToolResultError, GetArguments,
// MakeCallToolRequest, and the type aliases) are duplicated in
// internal/mcp/tools/compat.go. This is intentional — this package imports
// the tools sub-package, so tools cannot import back without creating a
// circular dependency. Both copies import the SDK types directly and must be
// kept in sync.
//
// This file additionally defines SchemaProperty and ToolSchema, which are only
// used within this package (not duplicated in tools/).

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolHandlerFunc is the handler signature for MCP tool handlers.
type ToolHandlerFunc = mcp.ToolHandler

// NewToolResultText creates a successful CallToolResult with a single text content.
func NewToolResultText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// NewToolResultError creates an error CallToolResult with a single text content.
func NewToolResultError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: true,
	}
}

// GetArguments extracts arguments from a CallToolRequest as map[string]any.
// Returns an empty (non-nil) map if arguments are nil or invalid.
func GetArguments(req *mcp.CallToolRequest) map[string]any {
	if req.Params == nil || req.Params.Arguments == nil {
		return make(map[string]any)
	}
	var args map[string]any
	if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
		return make(map[string]any)
	}
	return args
}

// MakeCallToolRequest creates a CallToolRequest with the given name and arguments.
// Used for internal dispatching (e.g., gateway → inner handler).
func MakeCallToolRequest(name string, args map[string]any) *mcp.CallToolRequest {
	var rawArgs json.RawMessage
	if args != nil {
		rawArgs, _ = json.Marshal(args)
	}
	return &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      name,
			Arguments: rawArgs,
		},
	}
}

// SchemaProperty describes a JSON schema property.
type SchemaProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// ToolSchema builds a JSON schema object for tool InputSchema from property
// definitions and required field names.
func ToolSchema(properties map[string]SchemaProperty, required []string) map[string]any {
	props := make(map[string]any, len(properties))
	for name, p := range properties {
		prop := map[string]any{"type": p.Type}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		props[name] = prop
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
