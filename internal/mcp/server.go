package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// Deps holds the dependencies for the MCP server.
type Deps struct {
	Registry     *connector.Registry
	WatcherStore store.WatcherStore
	AlertStore   store.AlertStore
}

// Serve starts a stdio-based MCP server that exposes all tools from the
// given connector registry plus watcher/alert management tools.
// It blocks until the connection is closed.
func Serve(deps Deps) error {
	s := server.NewMCPServer(
		"opentrace",
		"0.1.0",
		server.WithToolCapabilities(false),
	)

	// Convert and register all connector tools.
	for _, t := range deps.Registry.AllTools() {
		s.AddTool(convertTool(t), bridgeHandler(t))
	}

	// Add meta-tool for listing available connectors.
	s.AddTool(
		mcp.NewTool("list_connectors",
			mcp.WithDescription("List all active OpenTrace connectors and their tools"),
		),
		listConnectorsHandler(deps.Registry),
	)

	// Add watcher management tools.
	if deps.WatcherStore != nil {
		s.AddTool(
			mcp.NewTool("list_watchers",
				mcp.WithDescription("List all configured watchers with their status"),
			),
			listWatchersHandler(deps.WatcherStore),
		)

		s.AddTool(
			mcp.NewTool("create_watcher",
				mcp.WithDescription("Create a new automated watcher that monitors logs on a schedule"),
				mcp.WithString("title", mcp.Required(), mcp.Description("Title for the watcher")),
				mcp.WithString("description", mcp.Required(), mcp.Description("Instructions for the monitoring agent \u2014 what to look for")),
				mcp.WithString("service", mcp.Description("Filter by service name")),
				mcp.WithString("level", mcp.Description("Filter by log level (e.g. error, warning)")),
				mcp.WithString("environment", mcp.Description("Filter by environment (e.g. production)")),
				mcp.WithString("time_range", mcp.Description("Lookback window and run interval (e.g. 5m, 15m, 1h, 6h, 24h). Default: 15m")),
				mcp.WithString("query", mcp.Description("Full-text search query for logs")),
				mcp.WithString("severity", mcp.Description("Alert severity: info, warning, or critical (default: warning)")),
				mcp.WithString("model", mcp.Description("LLM model variant name (e.g. anthropic-sonnet, openai-gpt4o). Empty for global default")),
			),
			createWatcherHandler(deps.WatcherStore),
		)
	}

	// Add alert tools.
	if deps.AlertStore != nil {
		s.AddTool(
			mcp.NewTool("list_alerts",
				mcp.WithDescription("List recent alerts from watchers"),
				mcp.WithNumber("limit", mcp.Description("Maximum number of alerts to return (default: 10)")),
				mcp.WithBoolean("unread_only", mcp.Description("Only show unread alerts (default: false)")),
			),
			listAlertsHandler(deps.AlertStore),
		)
	}

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
// and their tools.
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

// listWatchersHandler returns a handler that lists all watchers.
func listWatchersHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		watchers, err := ws.List(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list watchers: %v", err)), nil
		}

		if len(watchers) == 0 {
			return mcp.NewToolResultText("No watchers configured."), nil
		}

		data, err := json.MarshalIndent(watchers, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal watchers: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

// createWatcherHandler returns a handler that creates a new watcher.
func createWatcherHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		title, _ := args["title"].(string)
		description, _ := args["description"].(string)
		if title == "" || description == "" {
			return mcp.NewToolResultError("title and description are required"), nil
		}

		// Build filters from individual params.
		filters := make(map[string]string)
		if v, ok := args["service"].(string); ok && v != "" {
			filters["service"] = v
		}
		if v, ok := args["level"].(string); ok && v != "" {
			filters["level"] = v
		}
		if v, ok := args["environment"].(string); ok && v != "" {
			filters["environment"] = v
		}
		if v, ok := args["query"].(string); ok && v != "" {
			filters["query"] = v
		}

		filtersJSON, _ := json.Marshal(filters)

		timeRange := "15m"
		if v, ok := args["time_range"].(string); ok && v != "" {
			timeRange = v
		}

		severity := store.SeverityWarning
		if v, ok := args["severity"].(string); ok && v != "" {
			severity = store.WatcherSeverity(v)
		}

		model, _ := args["model"].(string)

		params := store.CreateWatcherParams{
			Title:       title,
			Description: description,
			Severity:    severity,
			Filters:     filtersJSON,
			TimeRange:   timeRange,
			Model:       model,
			Notify:      json.RawMessage(`["dashboard"]`),
		}

		watcher, err := ws.Create(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create watcher: %v", err)), nil
		}

		data, err := json.MarshalIndent(watcher, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal watcher: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Watcher created successfully:\n%s", string(data))), nil
	}
}

// listAlertsHandler returns a handler that lists recent alerts.
func listAlertsHandler(as store.AlertStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		limit := 10
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}

		unreadOnly := false
		if v, ok := args["unread_only"].(bool); ok {
			unreadOnly = v
		}

		alerts, err := as.List(ctx, store.ListAlertParams{
			UnreadOnly: unreadOnly,
			Limit:      limit,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list alerts: %v", err)), nil
		}

		if len(alerts) == 0 {
			return mcp.NewToolResultText("No alerts found."), nil
		}

		data, err := json.MarshalIndent(alerts, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal alerts: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}
