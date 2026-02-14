package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/llm"
	"github.com/adham90/opentrace/internal/watcher"
)

// listModelsTool returns the tool definition for list_models.
func listModelsTool() mcp.Tool {
	return mcp.NewTool("list_models",
		mcp.WithDescription("List available LLM models and providers configured for AI watchers. Shows provider name, available models, and status. Use before creating AI watchers to pick the right model."),
	)
}

// listModelsHandler returns a handler that lists available LLM providers and models.
func listModelsHandler(cfg *config.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		providers := llm.AvailableProviders(cfg)
		if len(providers) == 0 {
			return mcp.NewToolResultText("No LLM providers are configured. Set OPENTRACE_LLM_PROVIDER and the corresponding API key."), nil
		}

		data, err := json.MarshalIndent(providers, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal models: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// watcherTemplatesTool returns the tool definition for watcher_templates.
func watcherTemplatesTool() mcp.Tool {
	return mcp.NewTool("watcher_templates",
		mcp.WithDescription("Get pre-built watcher configurations organized by category. Returns ready-to-use rule configs that can be passed directly to create_watcher. Categories include: database health, performance, security, and application."),
	)
}

// watcherTemplatesHandler returns a handler that lists builtin watcher templates.
func watcherTemplatesHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		templates := watcher.BuiltinTemplates()
		if len(templates) == 0 {
			return mcp.NewToolResultText("No watcher templates available."), nil
		}

		data, err := json.MarshalIndent(templates, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal templates: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
