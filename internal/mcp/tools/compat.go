package tools

// compat.go provides helper functions that bridge common patterns from the
// old mark3labs/mcp-go SDK to the new modelcontextprotocol/go-sdk. This makes
// the migration largely mechanical across tool files.
//
// NOTE: These helpers duplicate the ones in internal/mcp/compat.go.
// This is intentional — the parent mcp package imports this tools package
// (via server_factories.go and server_tools.go), so importing mcp from here
// would create a circular dependency. Both copies import the SDK types
// directly and must be kept in sync.

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolHandlerFunc is the handler signature for MCP tool handlers.
type ToolHandlerFunc = mcp.ToolHandler

// CallToolRequest is the MCP tool call request type.
type CallToolRequest = mcp.CallToolRequest

// CallToolResult is the MCP tool call result type.
type CallToolResult = mcp.CallToolResult

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
// Used for internal dispatching (e.g., unified handler → sub-handler).
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
