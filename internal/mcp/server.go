package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// Deps holds the dependencies for the MCP server.
type Deps struct {
	Registry      *connector.Registry
	WatcherStore  store.WatcherStore
	AlertStore    store.AlertStore
	ServerStore   store.ServerStore
	MetricStore   store.MetricStore
	UserStore     store.UserStore
	RuleEvaluator *watcher.RuleEvaluator
	MCPToken      string // OPENTRACE_MCP_TOKEN from environment
	ServerName    string // OPENTRACE_MCP_NAME — custom server name (default: "opentrace")
}

// Serve starts a stdio-based MCP server that exposes all tools from the
// given connector registry plus watcher/alert management tools.
// It blocks until the connection is closed.
//
// When UserStore and MCPToken are provided, the token is validated against the
// user database. Invalid/disabled tokens result in zero tools being registered
// (the server stays alive but is useless). Members get read-only tools; admins
// get all tools. When no UserStore is provided (backward compat), all tools
// are registered.
func Serve(deps Deps) error {
	name := deps.ServerName
	if name == "" {
		name = "opentrace"
	}

	s := server.NewMCPServer(
		name,
		"0.1.0",
		server.WithToolCapabilities(false),
	)

	// Determine access level.
	isAdmin := true    // default: full access (backward compat)
	hasAccess := true

	if deps.UserStore != nil && deps.MCPToken != "" {
		ctx := context.Background()
		user, err := deps.UserStore.GetByMCPToken(ctx, deps.MCPToken)
		if err != nil || user == nil {
			// Invalid token — serve with zero tools.
			hasAccess = false
		} else {
			isAdmin = user.Role == store.RoleAdmin
		}
	}

	if !hasAccess {
		// Start server with zero tools — stays alive but useless.
		return server.ServeStdio(s)
	}

	// Read-only tools (available to all authenticated users).
	addReadOnlyTools(s, deps)

	// Write tools (admin only).
	if isAdmin {
		addWriteTools(s, deps)
	}

	return server.ServeStdio(s)
}

// addReadOnlyTools registers read-only tools available to all users.
func addReadOnlyTools(s *server.MCPServer, deps Deps) {
	// Meta-tool for listing available connectors.
	s.AddTool(
		mcp.NewTool("list_connectors",
			mcp.WithDescription("List all active OpenTrace connectors and their tools"),
		),
		listConnectorsHandler(deps.Registry),
	)

	// Monitor list.
	if deps.WatcherStore != nil {
		s.AddTool(
			mcp.NewTool("list_monitors",
				mcp.WithDescription("List all configured monitors with their status"),
				mcp.WithString("environment", mcp.Description("Filter by environment (e.g. production, staging)")),
				mcp.WithString("monitor_type", mcp.Description("Filter by monitor type: ai or rule")),
			),
			listMonitorsHandler(deps.WatcherStore),
		)
	}

	// Alert list.
	if deps.AlertStore != nil {
		s.AddTool(
			mcp.NewTool("list_alerts",
				mcp.WithDescription("List recent alerts from watchers"),
				mcp.WithNumber("limit", mcp.Description("Maximum number of alerts to return (default: 10)")),
				mcp.WithBoolean("unread_only", mcp.Description("Only show unread alerts (default: false)")),
				mcp.WithString("environment", mcp.Description("Filter by environment (e.g. production, staging)")),
			),
			listAlertsHandler(deps.AlertStore),
		)
	}

	// Database introspection tools (Postgres runtime stats).
	s.AddTool(
		mcp.NewTool("db_query_stats",
			mcp.WithDescription("Show top SQL queries from pg_stat_statements — useful for identifying slow or frequent queries to monitor"),
			mcp.WithString("order_by", mcp.Description("Sort by: calls, total_exec_time (default), mean_exec_time, rows, shared_blks_hit, shared_blks_read")),
			mcp.WithNumber("limit", mcp.Description("Number of queries to return (default: 20, max: 100)")),
		),
		queryStatsHandler(deps.Registry),
	)

	// Server metrics read tools.
	if deps.ServerStore != nil && deps.MetricStore != nil {
		s.AddTool(
			mcp.NewTool("list_servers",
				mcp.WithDescription("List all monitored servers with their status (online/offline/unknown)"),
			),
			listServersHandler(deps.ServerStore),
		)

		s.AddTool(
			mcp.NewTool("query_metrics",
				mcp.WithDescription("Query time-series metrics for a server (CPU, memory, disk, network, load)"),
				mcp.WithString("server_id", mcp.Required(), mcp.Description("Server UUID (from list_servers)")),
				mcp.WithString("metric_name", mcp.Description("Metric name filter (e.g. cpu.usage_percent, memory.usage_percent)")),
				mcp.WithString("start", mcp.Description("Start time in ISO 8601 format")),
				mcp.WithString("end", mcp.Description("End time in ISO 8601 format")),
				mcp.WithNumber("limit", mcp.Description("Max results (default: 100)")),
			),
			queryMetricsHandler(deps.ServerStore, deps.MetricStore),
		)

		s.AddTool(
			mcp.NewTool("server_health",
				mcp.WithDescription("Get current health snapshot for a server — latest value for every metric"),
				mcp.WithString("server_id", mcp.Required(), mcp.Description("Server UUID (from list_servers)")),
			),
			serverHealthHandler(deps.ServerStore, deps.MetricStore),
		)
	}
}

// addWriteTools registers write/admin tools (connector tools, create_monitor, preview_monitor).
func addWriteTools(s *server.MCPServer, deps Deps) {
	// All connector tools (run queries, etc.).
	for _, t := range deps.Registry.AllTools() {
		s.AddTool(convertTool(t), bridgeHandler(t))
	}

	// Create monitor.
	if deps.WatcherStore != nil {
		s.AddTool(
			mcp.NewTool("create_monitor",
				mcp.WithDescription("Create a new monitor. Use monitor_type=ai for AI-powered analysis or monitor_type=rule for threshold-based checks"),
				mcp.WithString("title", mcp.Required(), mcp.Description("Title for the monitor")),
				mcp.WithString("monitor_type", mcp.Description("Monitor type: ai (default) or rule")),
				mcp.WithString("description", mcp.Description("Instructions for the AI agent (required for ai monitors)")),
				mcp.WithString("rule_config", mcp.Description("JSON object for rule monitors: {source, query, metric, operator, threshold, filter, checks, latency_threshold_ms}")),
				mcp.WithString("data_source_id", mcp.Description("Data source ID for query/health rule monitors")),
				mcp.WithString("service", mcp.Description("Filter by service name (ai monitors)")),
				mcp.WithString("level", mcp.Description("Filter by log level (ai monitors)")),
				mcp.WithString("environment", mcp.Description("Filter by environment (e.g. production)")),
				mcp.WithString("time_range", mcp.Description("Check interval (e.g. 5m, 15m, 1h). Default: 15m")),
				mcp.WithString("query", mcp.Description("Full-text search query for logs (ai monitors)")),
				mcp.WithString("severity", mcp.Description("Alert severity: info, warning, or critical (default: warning)")),
				mcp.WithString("model", mcp.Description("LLM model name (ai monitors only)")),
				mcp.WithString("effort", mcp.Description("Analysis effort: low, medium (default), or high (ai monitors only)")),
				mcp.WithString("watcher_environment", mcp.Description("Environment to assign to the monitor (e.g. production, staging)")),
			),
			createMonitorHandler(deps.WatcherStore),
		)
	}

	// Preview monitor (rule evaluation without saving).
	if deps.RuleEvaluator != nil {
		s.AddTool(
			mcp.NewTool("preview_monitor",
				mcp.WithDescription("Run a rule monitor evaluation ad-hoc without saving. Returns the current value and whether it would trigger an alert"),
				mcp.WithString("rule_config", mcp.Required(), mcp.Description("JSON object: {source, query, metric, operator, threshold, filter, checks, latency_threshold_ms}")),
				mcp.WithString("data_source_id", mcp.Description("Data source ID for query/health rule monitors")),
			),
			previewMonitorHandler(deps.RuleEvaluator),
		)
	}
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

// listMonitorsHandler returns a handler that lists all monitors.
func listMonitorsHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		var params store.ListWatcherParams
		if v, ok := args["environment"].(string); ok && v != "" {
			params.Environment = v
		}
		if v, ok := args["monitor_type"].(string); ok && v != "" {
			params.MonitorType = store.MonitorType(v)
		}
		watchers, err := ws.List(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list monitors: %v", err)), nil
		}

		if len(watchers) == 0 {
			return mcp.NewToolResultText("No monitors configured."), nil
		}

		data, err := json.MarshalIndent(watchers, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal monitors: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

// createMonitorHandler returns a handler that creates a new monitor (AI or rule).
func createMonitorHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		title, _ := args["title"].(string)
		if title == "" {
			return mcp.NewToolResultError("title is required"), nil
		}

		monitorType := store.MonitorTypeAI
		if v, ok := args["monitor_type"].(string); ok && v != "" {
			monitorType = store.MonitorType(v)
		}

		description, _ := args["description"].(string)
		if monitorType == store.MonitorTypeAI && description == "" {
			return mcp.NewToolResultError("description is required for ai monitors"), nil
		}

		// Build filters from individual params (for AI monitors).
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

		effort := store.EffortMedium
		if v, ok := args["effort"].(string); ok && v != "" {
			effort = store.WatcherEffort(v)
		}

		watcherEnv, _ := args["watcher_environment"].(string)

		params := store.CreateWatcherParams{
			Title:       title,
			Description: description,
			Environment: watcherEnv,
			MonitorType: monitorType,
			Severity:    severity,
			Filters:     filtersJSON,
			TimeRange:   timeRange,
			Model:       model,
			Effort:      effort,
			Notify:      json.RawMessage(`["dashboard"]`),
		}

		// Parse rule_config JSON string for rule monitors.
		if monitorType == store.MonitorTypeRule {
			rcStr, _ := args["rule_config"].(string)
			if rcStr == "" {
				return mcp.NewToolResultError("rule_config is required for rule monitors"), nil
			}
			var rc store.RuleConfig
			if err := json.Unmarshal([]byte(rcStr), &rc); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid rule_config JSON: %v", err)), nil
			}
			params.RuleConfig = &rc
		}

		if v, ok := args["data_source_id"].(string); ok && v != "" {
			params.DataSourceID = &v
		}

		monitor, err := ws.Create(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create monitor: %v", err)), nil
		}

		data, err := json.MarshalIndent(monitor, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal monitor: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Monitor created successfully:\n%s", string(data))), nil
	}
}

// previewMonitorHandler returns a handler that runs a rule evaluation ad-hoc.
func previewMonitorHandler(re *watcher.RuleEvaluator) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		rcStr, _ := args["rule_config"].(string)
		if rcStr == "" {
			return mcp.NewToolResultError("rule_config is required"), nil
		}

		var rc store.RuleConfig
		if err := json.Unmarshal([]byte(rcStr), &rc); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid rule_config JSON: %v", err)), nil
		}

		tempWatcher := store.Watcher{
			MonitorType: store.MonitorTypeRule,
			RuleConfig:  &rc,
		}
		if v, ok := args["data_source_id"].(string); ok && v != "" {
			tempWatcher.DataSourceID = &v
		}

		start := time.Now()
		result, err := re.Evaluate(ctx, tempWatcher)
		elapsed := time.Since(start).Milliseconds()

		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Preview error: %v (took %dms)", err, elapsed)), nil
		}

		resp := map[string]any{
			"would_alert":   result.HasAlert,
			"summary":       result.Summary,
			"query_time_ms": elapsed,
		}
		if result.Value != nil {
			resp["current_value"] = *result.Value
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// listServersHandler returns a handler that lists all monitored servers.
func listServersHandler(ss store.ServerStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		servers, err := ss.List(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list servers: %v", err)), nil
		}
		if len(servers) == 0 {
			return mcp.NewToolResultText("No monitored servers."), nil
		}
		data, err := json.MarshalIndent(servers, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal servers: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// queryMetricsHandler returns a handler that queries time-series metrics.
func queryMetricsHandler(ss store.ServerStore, ms store.MetricStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		serverIDStr, _ := args["server_id"].(string)
		if serverIDStr == "" {
			return mcp.NewToolResultError("server_id is required"), nil
		}

		serverID, err := uuid.Parse(serverIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid server_id format"), nil
		}

		q := store.MetricQuery{ServerID: serverID}
		if v, ok := args["metric_name"].(string); ok {
			q.MetricName = v
		}
		if v, ok := args["start"].(string); ok && v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				q.Start = &t
			}
		}
		if v, ok := args["end"].(string); ok && v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				q.End = &t
			}
		}
		if v, ok := args["limit"].(float64); ok && v > 0 {
			q.Limit = int(v)
		}

		points, err := ms.Query(ctx, q)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query metrics: %v", err)), nil
		}
		if len(points) == 0 {
			return mcp.NewToolResultText("No metrics found matching the given criteria."), nil
		}

		data, err := json.MarshalIndent(points, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal metrics: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// serverHealthHandler returns a handler that shows the latest metrics for a server.
func serverHealthHandler(ss store.ServerStore, ms store.MetricStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		serverIDStr, _ := args["server_id"].(string)
		if serverIDStr == "" {
			return mcp.NewToolResultError("server_id is required"), nil
		}

		serverID, err := uuid.Parse(serverIDStr)
		if err != nil {
			return mcp.NewToolResultError("invalid server_id format"), nil
		}

		srv, err := ss.GetByID(ctx, serverID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("server not found: %v", err)), nil
		}

		latest, err := ms.LatestByServer(ctx, serverID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query metrics: %v", err)), nil
		}

		result := map[string]any{
			"server":  srv,
			"metrics": latest,
		}
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
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

		var environment string
		if v, ok := args["environment"].(string); ok {
			environment = v
		}

		alerts, err := as.List(ctx, store.ListAlertParams{
			UnreadOnly:  unreadOnly,
			Environment: environment,
			Limit:       limit,
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
