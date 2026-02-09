package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/opentrace/opentrace/internal/agent"
	"github.com/opentrace/opentrace/internal/connector"
)

// Serve starts a stdio-based MCP server that exposes all tools from the
// given connector registry. It blocks until the connection is closed.
func Serve(registry *connector.Registry) error {
	s := server.NewMCPServer(
		"opentrace",
		"0.1.0",
		server.WithToolCapabilities(false),
	)

	// Convert and register all connector tools.
	for _, t := range registry.AllTools() {
		s.AddTool(convertTool(t), bridgeHandler(t))
	}

	// Add meta-tool for listing available connectors.
	s.AddTool(
		mcp.NewTool("list_connectors",
			mcp.WithDescription("List all active OpenTrace connectors and their tools"),
		),
		listConnectorsHandler(registry),
	)

	return server.ServeStdio(s)
}

// convertTool maps an agent.Tool to an mcp.Tool with the appropriate
// JSON Schema properties derived from the tool's parameter definitions.
func convertTool(t agent.Tool) mcp.Tool {
	opts := []mcp.ToolOption{
		mcp.WithDescription(t.Description),
	}

	for _, p := range t.Params {
		var propOpts []mcp.PropertyOption
		if p.Required {
			propOpts = append(propOpts, mcp.Required())
		}

		switch p.Type {
		case "string":
			opts = append(opts, mcp.WithString(p.Name, propOpts...))
		case "int":
			opts = append(opts, mcp.WithNumber(p.Name, propOpts...))
		case "bool":
			opts = append(opts, mcp.WithBoolean(p.Name, propOpts...))
		default:
			opts = append(opts, mcp.WithString(p.Name, propOpts...))
		}
	}

	return mcp.NewTool(t.Name, opts...)
}

// bridgeHandler wraps an agent.Tool handler as an MCP ToolHandlerFunc.
// Tool-level errors are returned as MCP error results (not transport errors).
func bridgeHandler(t agent.Tool) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		if args == nil {
			args = make(map[string]any)
		}

		result, err := t.Handler(ctx, args)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(result), nil
	}
}

// listConnectorsHandler returns a handler that lists all active connectors
// and their tools. This helps the MCP client understand what data sources
// are available.
func listConnectorsHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tools := registry.AllTools()
		if len(tools) == 0 {
			return mcp.NewToolResultText("No connectors are currently active."), nil
		}

		var b strings.Builder
		b.WriteString(fmt.Sprintf("Active tools (%d):\n", len(tools)))
		for _, t := range tools {
			b.WriteString(fmt.Sprintf("- %s: %s\n", t.Name, t.Description))
		}

		return mcp.NewToolResultText(b.String()), nil
	}
}
