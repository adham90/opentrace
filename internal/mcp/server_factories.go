package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/metrics"
	"github.com/adham90/opentrace/pkg/store"
)

// wrapWithMetrics wraps a tool handler to record Prometheus metrics for each call.
func wrapWithMetrics(toolName string, handler ToolHandlerFunc) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		metrics.RecordMCPToolCall(toolName)
		return handler(ctx, request)
	}
}

// wrapWithActivityLog wraps a tool handler to log its execution to the activity store.
func wrapWithActivityLog(deps Deps, toolName string, handler ToolHandlerFunc) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		result, err := handler(ctx, request)
		elapsed := time.Since(start).Milliseconds()

		// Build a brief preview of args
		argsPreview := ""
		if args := GetArguments(request); len(args) > 0 {
			data, _ := json.Marshal(args)
			argsPreview = string(data)
			if len(argsPreview) > 500 {
				argsPreview = argsPreview[:500]
			}
		}

		// Build result preview
		isError := err != nil
		resultPreview := ""
		if result != nil && len(result.Content) > 0 {
			if txt, ok := result.Content[0].(*mcp.TextContent); ok {
				resultPreview = txt.Text
				if len(resultPreview) > 500 {
					resultPreview = resultPreview[:500]
				}
			}
			isError = isError || result.IsError
		}

		sessionID := "mcp"

		// Log via bounded activity logger to avoid unbounded goroutine growth.
		if deps.ActivityLogger != nil {
			deps.ActivityLogger.Log(store.LogMCPActivityParams{
				SessionID:     sessionID,
				ToolName:      toolName,
				Arguments:     argsPreview,
				ResultPreview: resultPreview,
				IsError:       isError,
				DurationMs:    &elapsed,
				EventType:     "tool_call",
			})
		}

		return result, err
	}
}

// wrapHandler applies activity logging and metrics to a handler.
func wrapHandler(deps Deps, toolName string, handler ToolHandlerFunc) ToolHandlerFunc {
	handler = wrapWithMetrics(toolName, handler)
	if deps.MCPActivityStore != nil {
		handler = wrapWithActivityLog(deps, toolName, handler)
	}
	return handler
}

// ---------------------------------------------------------------------------
// Legacy helpers (still needed for dynamic connector tools)
// ---------------------------------------------------------------------------

// convertTool maps a connector.Tool to an mcp.Tool with the appropriate
// JSON Schema properties derived from the tool's parameter definitions.
func convertTool(t connector.Tool) *mcp.Tool {
	props := make(map[string]SchemaProperty)
	var required []string
	for _, p := range t.Params {
		schemaType := "string"
		switch p.Type {
		case "int":
			schemaType = "number"
		case "bool":
			schemaType = "boolean"
		}
		props[p.Name] = SchemaProperty{Type: schemaType}
		if p.Required {
			required = append(required, p.Name)
		}
	}
	return &mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: ToolSchema(props, required),
	}
}

// bridgeHandler wraps a connector.Tool handler as an MCP ToolHandlerFunc.
func bridgeHandler(t connector.Tool) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := GetArguments(request)
		if args == nil {
			args = make(map[string]any)
		}

		result, err := t.Handler(ctx, args)
		if err != nil {
			return NewToolResultError(err.Error()), nil
		}

		return NewToolResultText(result), nil
	}
}
