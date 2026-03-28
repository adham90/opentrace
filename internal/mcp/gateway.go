package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// quiet and detail params are documented in the gateway tool description.

// Gateway is the lazy tool loading gateway. It exposes a single MCP tool
// ("opentrace") that dispatches to internal tool handlers based on the
// "tool" and "action" parameters. This dramatically reduces the context
// window usage for coding agents: instead of 14+ tool schemas, the agent
// sees just 1.
type Gateway struct {
	handlers  map[string]server.ToolHandlerFunc
	entries   []GatewayEntry
	mcpServer *server.MCPServer // set after registration, for elicitation
}

// GatewayEntry describes a tool registered in the gateway (for discover).
type GatewayEntry struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Actions     []string          `json:"actions,omitempty"`
	Category    string            `json:"category"`
	Access      string            `json:"access"`
	ReadOnly    bool              `json:"read_only"`
	Destructive bool              `json:"destructive,omitempty"`
	Idempotent  bool              `json:"idempotent,omitempty"`
	Params      map[string]string `json:"params,omitempty"` // action-specific param docs
}

// NewGateway creates a new Gateway.
func NewGateway() *Gateway {
	return &Gateway{
		handlers: make(map[string]server.ToolHandlerFunc),
	}
}

// Register adds an internal tool handler to the gateway.
func (g *Gateway) Register(name string, handler server.ToolHandlerFunc, entry GatewayEntry) {
	g.handlers[name] = handler
	entry.Name = name
	g.entries = append(g.entries, entry)
}

// Tool returns the single MCP tool definition for the gateway.
// Item 1: Tool annotations (readOnlyHint, openWorldHint).
// Item 2: Trimmed description — no inline tool list.
func (g *Gateway) Tool() mcplib.Tool {
	return mcplib.NewTool("opentrace",
		mcplib.WithDescription(
			`OpenTrace monitoring gateway. Use tool="discover" to list all tools. `+
				`Use tool="describe" action="<name>" for param docs. `+
				`Execute: tool=<name> action=<action> params={...}. `+
				`All tools accept "since" for time range (e.g. "1h", "24h", "7d"). `+
				`Add quiet=true to omit suggestions. Add detail="brief" for compact output.`),
		mcplib.WithString("tool", mcplib.Required(),
			mcplib.Description("Tool name, 'discover', or 'describe'")),
		mcplib.WithString("action",
			mcplib.Description("Action to perform (or tool name for describe)")),
		mcplib.WithObject("params",
			mcplib.Description("Action-specific parameters (all accept 'since' for time range)")),
		mcplib.WithBoolean("quiet",
			mcplib.Description("Omit suggested_tools and investigation_context from response")),
		mcplib.WithString("detail",
			mcplib.Description("Response detail: 'brief' (compact, truncated arrays) or 'full' (default)")),
		// Item 1: Annotations — conservative defaults for the gateway.
		mcplib.WithReadOnlyHintAnnotation(false),
		mcplib.WithDestructiveHintAnnotation(false),
		mcplib.WithIdempotentHintAnnotation(false),
		mcplib.WithOpenWorldHintAnnotation(true),
	)
}

// Handler returns the MCP handler for the gateway tool.
func (g *Gateway) Handler() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		args := request.GetArguments()
		toolName, _ := args["tool"].(string)

		switch toolName {
		case "", "discover":
			return g.handleDiscover(args)
		case "describe":
			return g.handleDescribe(args)
		}

		handler, ok := g.handlers[toolName]
		if !ok {
			var valid []string
			for _, e := range g.entries {
				valid = append(valid, e.Name)
			}
			sort.Strings(valid)
			return mcplib.NewToolResultError(fmt.Sprintf(
				"unknown tool %q. Available: %s. Use tool=\"discover\" for details.",
				toolName, strings.Join(valid, ", "))), nil
		}

		// Build the inner request arguments.
		action, _ := args["action"].(string)
		params, _ := args["params"].(map[string]any)
		if params == nil {
			params = make(map[string]any)
		}
		if action != "" {
			params["action"] = action
		}

		innerReq := mcplib.CallToolRequest{}
		innerReq.Params.Name = toolName
		innerReq.Params.Arguments = params

		result, err := handler(ctx, innerReq)
		if err != nil {
			return result, err
		}

		// Post-processing: quiet mode and detail level.
		quiet, _ := args["quiet"].(bool)
		if !quiet {
			// Also check inside params for backward compat.
			quiet, _ = params["quiet"].(bool)
		}
		detail, _ := args["detail"].(string)
		if detail == "" {
			detail, _ = params["detail"].(string)
		}

		if quiet || detail == "brief" {
			result = g.postProcess(result, quiet, detail)
		}

		return result, nil
	}
}

// postProcess applies quiet mode and detail level to a tool result.
func (g *Gateway) postProcess(result *mcplib.CallToolResult, quiet bool, detail string) *mcplib.CallToolResult {
	if result == nil || result.IsError {
		return result
	}

	// Parse the JSON text content.
	for i, content := range result.Content {
		tc, ok := content.(mcplib.TextContent)
		if !ok {
			continue
		}

		var data map[string]any
		if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
			continue
		}

		modified := false

		// Quiet mode: remove suggested_tools and investigation_context.
		if quiet {
			if _, ok := data["suggested_tools"]; ok {
				delete(data, "suggested_tools")
				modified = true
			}
			if _, ok := data["investigation_context"]; ok {
				delete(data, "investigation_context")
				modified = true
			}
		}

		// Brief mode: truncate arrays to 3 items, shorten messages.
		if detail == "brief" {
			modified = truncateForBrief(data) || modified
		}

		if modified {
			newJSON, err := json.Marshal(data)
			if err == nil {
				tc.Text = string(newJSON)
				result.Content[i] = tc
			}
		}
	}

	return result
}

// truncateForBrief truncates arrays to 3 items and adds counts.
func truncateForBrief(data map[string]any) bool {
	modified := false
	for key, val := range data {
		switch v := val.(type) {
		case []any:
			if len(v) > 3 {
				data[key] = v[:3]
				data[key+"_total"] = len(v)
				data[key+"_truncated"] = true
				modified = true
			}
		}
	}
	return modified
}

// handleDiscover returns a compact catalog of available tools.
func (g *Gateway) handleDiscover(args map[string]any) (*mcplib.CallToolResult, error) {
	categoryFilter, _ := args["action"].(string)

	var filtered []GatewayEntry
	for _, e := range g.entries {
		if categoryFilter != "" && categoryFilter != "discover" &&
			!strings.EqualFold(e.Category, categoryFilter) {
			continue
		}
		filtered = append(filtered, e)
	}

	// Group by category.
	type categoryGroup struct {
		Category string         `json:"category"`
		Tools    []GatewayEntry `json:"tools"`
	}

	categoryOrder := make([]string, 0)
	grouped := make(map[string][]GatewayEntry)
	for _, e := range filtered {
		cat := e.Category
		if cat == "" {
			cat = "Other"
		}
		if _, seen := grouped[cat]; !seen {
			categoryOrder = append(categoryOrder, cat)
		}
		grouped[cat] = append(grouped[cat], e)
	}

	groups := make([]categoryGroup, 0, len(categoryOrder))
	for _, cat := range categoryOrder {
		groups = append(groups, categoryGroup{
			Category: cat,
			Tools:    grouped[cat],
		})
	}

	resp := map[string]any{
		"total_tools": len(filtered),
		"categories":  groups,
		"usage":       `opentrace(tool="<name>", action="<action>", params={...})`,
		"tip":         `Use tool="describe" action="<tool_name>" for full parameter documentation.`,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("failed to marshal catalog: %v", err)), nil
	}
	return mcplib.NewToolResultText(string(data)), nil
}

// SetServer stores the MCPServer reference for elicitation support (Item 14).
// Called after the gateway tool is registered on the server.
func (g *Gateway) SetServer(s *server.MCPServer) {
	g.mcpServer = s
}

// Elicit requests structured user input via the MCP Elicitation primitive.
// Returns nil, nil if elicitation is not supported by the client.
func (g *Gateway) Elicit(ctx context.Context, message string, schema any) (map[string]any, error) {
	if g.mcpServer == nil {
		return nil, nil
	}

	result, err := g.mcpServer.RequestElicitation(ctx, mcplib.ElicitationRequest{
		Params: mcplib.ElicitationParams{
			Message:         message,
			RequestedSchema: schema,
		},
	})
	if err != nil {
		return nil, nil // elicitation not supported — fall back to error
	}
	if result == nil || result.Action != "accept" {
		return nil, nil
	}

	if content, ok := result.Content.(map[string]any); ok {
		return content, nil
	}
	return nil, nil
}

// handleDescribe returns full parameter documentation for a specific tool.
// Item 3: Schema-on-demand — avoids embedding all schemas in the gateway description.
func (g *Gateway) handleDescribe(args map[string]any) (*mcplib.CallToolResult, error) {
	targetTool, _ := args["action"].(string)
	if targetTool == "" {
		return mcplib.NewToolResultError("specify the tool name in the action parameter: tool=\"describe\" action=\"logs\""), nil
	}

	for _, e := range g.entries {
		if e.Name == targetTool {
			resp := map[string]any{
				"name":        e.Name,
				"description": e.Description,
				"category":    e.Category,
				"access":      e.Access,
				"read_only":   e.ReadOnly,
				"destructive": e.Destructive,
				"idempotent":  e.Idempotent,
			}
			if len(e.Actions) > 0 {
				resp["actions"] = e.Actions
			}
			if len(e.Params) > 0 {
				resp["params"] = e.Params
			}
			data, _ := json.Marshal(resp)
			return mcplib.NewToolResultText(string(data)), nil
		}
	}

	return mcplib.NewToolResultError(fmt.Sprintf("unknown tool %q. Use tool=\"discover\" to see available tools.", targetTool)), nil
}
