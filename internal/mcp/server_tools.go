package mcp

import (
	"context"
	"encoding/json"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/mcp/tools"
	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Gateway-based tool registration
// ---------------------------------------------------------------------------

// buildGateway creates the Gateway with all tools registered.
// The CatalogBuilder is populated in parallel for the web UI.
func buildGateway(deps Deps, isAdmin bool, b *CatalogBuilder) *Gateway {
	gw := NewGateway()

	registerReadOnlyTools(gw, deps, b)
	if isAdmin {
		registerWriteTools(gw, deps, b)
	}

	return gw
}

// registerReadOnlyTools adds read-only tools to the gateway.
func registerReadOnlyTools(gw *Gateway, deps Deps, b *CatalogBuilder) {
	// --- connectors ---
	gw.Register("connectors",
		wrapHandler("connectors", tools.ConnectorsHandler(tools.ConnectorsDeps{
			DSStore:       deps.DSStore,
			Registry:      deps.Registry,
			LogStore:      deps.LogStore,
			Config:        deps.Config,
			SettingsStore: deps.SettingsStore,
		})),
		GatewayEntry{
			Description: "Connector management: list, get, create, test, update, delete",
			Actions:     []string{"list", "get", "create", "test", "update", "delete"},
			Category:    "Connectors",
			Access:      "read",
			ReadOnly:    false,
			Destructive: true,
			Params:      map[string]string{"type": "Filter by connector type", "id": "Connector ID for get/update/delete"},
		})
	b.Add("connectors", "Connector management: list, get, create, test, update, delete", "Connectors", "read", "")

	// --- database (now includes runbook) ---
	gw.Register("database",
		wrapHandler("database", tools.DatabaseHandler(tools.DatabaseDeps{
			Registry:                  deps.Registry,
			QueryMemoryStore:          deps.QueryMemoryStore,
			LogStore:                  deps.LogStore,
			RunbookEffectivenessStore: deps.RunbookEffectivenessStore,
		})),
		GatewayEntry{
			Description: "Database introspection, management, and investigation runbooks",
			Actions:     []string{"queries", "explain", "tables", "activity", "locks", "connections", "indexes", "schema", "storage", "kill_query", "long_transactions", "runbook"},
			Category:    "Database",
			Access:      "read",
			ReadOnly:    false,
			Destructive: true,
			Params:      map[string]string{"order_by": "Sort: calls, total_exec_time, mean_exec_time", "query": "SQL query (explain)", "playbook": "Runbook: slow_database, connection_exhaustion, disk_pressure, replication_lag, error_spike", "pid": "PID to kill (kill_query)"},
		})
	b.Add("database", "Database introspection and management: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions, runbook", "Database Introspection", "read", "database connector")

	// --- logs ---
	if deps.LogStore != nil {
		var traceRecorder tools.TraceSessionRecorder
		if sessionTracker != nil {
			traceRecorder = func(traceID string) {
				if sess := sessionTracker.CurrentSession(); sess != nil {
					traceIDs := append([]string{}, sess.TraceIDs...)
					traceIDs = append(traceIDs, traceID)
					sessionTracker.UpdateSession(store.UpdateInvestigationSessionParams{
						TraceIDs: traceIDs,
					})
				}
			}
		}
		gw.Register("logs",
			wrapHandler("logs", tools.LogsHandler(tools.LogsDeps{
				LogStore:             deps.LogStore,
				ErrorGroupStore:      deps.ErrorGroupStore,
				TraceSessionRecorder: traceRecorder,
				Ranker:               rankingServiceAdapter(),
			})),
			GatewayEntry{
				Description: "Log intelligence: search, context, attributes, stats, summary, performance, trace, compare",
				Actions:     []string{"search", "context", "attributes", "stats", "summary", "performance", "trace", "compare"},
				Category:    "Log Intelligence",
				Access:      "read",
				ReadOnly:    true,
				Idempotent:  true,
				Params:      map[string]string{"query": "Full-text search query", "service": "Filter by service", "level": "debug/info/warn/error/fatal", "time_range": "15m/1h/6h/24h/7d", "trace_id": "Trace/correlation ID", "limit": "Max results (default 50)"},
			})
		b.Add("logs", "Unified log intelligence: search, context, attributes, stats, summary, performance, trace, compare", "Log Intelligence", "read", "")
	}

	// --- errors ---
	if deps.ErrorGroupStore != nil || deps.LogStore != nil || deps.ErrorImpactStore != nil {
		var sess tools.SessionInfo
		var rec tools.RecurrenceLinker
		if sessionTracker != nil {
			sess = sessionTracker
		}
		if recurrenceDetector != nil {
			rec = recurrenceDetector
		}
		gw.Register("errors",
			wrapHandler("errors", tools.ErrorsHandler(tools.ErrorsDeps{
				ErrorGroupStore:  deps.ErrorGroupStore,
				LogStore:         deps.LogStore,
				ErrorImpactStore: deps.ErrorImpactStore,
				Session:          sess,
				Recurrence:       rec,
				Ranker:           rankingServiceAdapter(),
			})),
			GatewayEntry{
				Description: "Error management: list, detail, investigate, impact, user_errors, ranking, resolve, ignore, new",
				Actions:     []string{"list", "detail", "investigate", "impact", "user_errors", "ranking", "resolve", "ignore", "new"},
				Category:    "Errors",
				Access:      "read",
				ReadOnly:    false,
				Params:      map[string]string{"fingerprint": "Error fingerprint", "service": "Filter by service", "status": "unresolved/resolved/ignored", "limit": "Max results"},
			})
		b.Add("errors", "Manage and investigate errors: list, detail, investigate, impact, user_errors, ranking, resolve, ignore, new", "Errors", "read", "")
	}

	// --- healthchecks ---
	if deps.HealthCheckStore != nil {
		gw.Register("healthchecks",
			wrapHandler("healthchecks", tools.HealthchecksHandler(tools.HealthchecksDeps{
				HealthCheckStore: deps.HealthCheckStore,
			})),
			GatewayEntry{
				Description: "Health check management: list, uptime, create, delete",
				Actions:     []string{"list", "uptime", "create", "delete"},
				Category:    "Uptime",
				Access:      "read",
				ReadOnly:    false,
				Destructive: true,
				Params:      map[string]string{"url": "Health check URL (create)", "id": "Health check ID (delete/uptime)"},
			})
		b.Add("healthchecks", "Health check management: list, uptime, create, delete", "Uptime", "read", "")
	}

	// --- overview (includes agent memory: notes + session_summary) ---
	var overviewSessionCallback tools.SessionSummaryCallback
	if deps.InvestigationSessionStore != nil && sessionTracker != nil {
		overviewSessionCallback = func(ctx context.Context, args map[string]any) (*mcplib.CallToolResult, error) {
			return handleSessionSummaryFromArgs(args)
		}
	}
	gw.Register("overview",
		wrapHandler("overview", tools.OverviewHandler(tools.OverviewDeps{
			LogStore:         deps.LogStore,
			DSStore:          deps.DSStore,
			ServerStore:      deps.ServerStore,
			ErrorGroupStore:  deps.ErrorGroupStore,
			WatchStore:       deps.WatchStore,
			HealthCheckStore: deps.HealthCheckStore,
			SettingsStore:    deps.SettingsStore,
			AgentNoteStore:   deps.AgentNoteStore,
			SessionSummary:   overviewSessionCallback,
		})),
		GatewayEntry{
			Description: "System overview, triage, diagnosis, incident timeline, agent memory, and session summary",
			Actions:     []string{"status", "triage", "diagnose", "timeline", "investigate", "changes", "settings", "notes", "delete_note", "session_summary"},
			Category:    "Overview",
			Access:      "read",
			ReadOnly:    true,
			Idempotent:  true,
			Params:      map[string]string{"service": "Scope to service", "timeframe": "Window: 30m/2h/24h/7d"},
		})
	b.Add("overview", "System overview, triage, diagnosis, incident timeline, service investigation, and changes", "Overview", "read", "")

	// --- watches ---
	if deps.WatchStore != nil {
		gw.Register("watches",
			wrapHandler("watches", tools.WatchesHandler(tools.WatchesDeps{
				WatchStore:   deps.WatchStore,
				LogStore:     deps.LogStore,
				WatchMetrics: deps.WatchMetrics,
			})),
			GatewayEntry{
				Description: "Watch management: status, create, delete, alerts, dismiss, acknowledge, investigate",
				Actions:     []string{"status", "create", "delete", "alerts", "dismiss", "acknowledge", "investigate"},
				Category:    "Watches",
				Access:      "read",
				ReadOnly:    false,
				Destructive: true,
				Params:      map[string]string{"name": "Watch name (create)", "id": "Watch ID", "service": "Filter by service"},
			})
		b.Add("watches", "Watch management: status, create, delete, alerts, dismiss, acknowledge, investigate", "Watches", "read", "")
	}

	// --- analytics ---
	if deps.AnalyticsStore != nil || deps.TrendStore != nil {
		gw.Register("analytics",
			wrapHandler("analytics", tools.AnalyticsHandler(tools.AnalyticsDeps{
				AnalyticsStore: deps.AnalyticsStore,
				TrendStore:     deps.TrendStore,
			})),
			GatewayEntry{
				Description: "Web analytics and trends: traffic, endpoints, heatmap, trends, movers",
				Actions:     []string{"traffic", "endpoints", "heatmap", "trends", "movers"},
				Category:    "Analytics",
				Access:      "read",
				ReadOnly:    true,
				Idempotent:  true,
				Params:      map[string]string{"service": "Filter by service", "time_range": "15m/1h/6h/24h/7d", "limit": "Max results"},
			})
		b.Add("analytics", "Web analytics and trends: traffic, endpoints, heatmap, trends, movers", "Analytics", "read", "")
	}

	// --- code (unified: code_intel + annotations + test_gen + dependencies) ---
	if deps.CodeEntityStore != nil || deps.TestCorrelationStore != nil ||
		deps.AnalyticsStore != nil || deps.ErrorGroupStore != nil {
		gw.Register("code",
			wrapHandler("code", tools.CodeHandler(tools.CodeDeps{
				CodeEntityStore:           deps.CodeEntityStore,
				ErrorGroupStore:           deps.ErrorGroupStore,
				ErrorImpactStore:          deps.ErrorImpactStore,
				TestCorrelationStore:      deps.TestCorrelationStore,
				DeployStore:               deps.DeployStore,
				AgentNoteStore:            deps.AgentNoteStore,
				InvestigationSessionStore: deps.InvestigationSessionStore,
				AnalyticsStore:            deps.AnalyticsStore,
				LogStore:                  deps.LogStore,
			})),
			GatewayEntry{
				Description: "Code intelligence, annotations, test generation, and dependency analysis",
				Actions: []string{
					"risk", "fragile", "context", "test_gaps", "test_priority",
					"annotate_file", "annotate_function", "hotspots",
					"gen_context", "gen_suggest", "gen_coverage",
					"deps_service", "deps_blast", "deps_risk",
				},
				Category: "Code Intelligence",
				Access:   "read",
				ReadOnly: true,
				Idempotent: true,
				Params:   map[string]string{"service": "Filter by service", "fingerprint": "Error fingerprint", "path": "Source file path", "files": "Array of file paths (risk)"},
			})
		b.Add("code", "Code intelligence, annotations, test generation, and dependency analysis", "Code Intelligence", "read", "")
	}

	// --- deploys ---
	if deps.DeployStore != nil {
		gw.Register("deploys",
			wrapHandler("deploys", tools.DeploysHandler(tools.DeploysDeps{
				DeployStore: deps.DeployStore,
			})),
			GatewayEntry{
				Description: "Deploy management: history, impact, record",
				Actions:     []string{"history", "impact", "record"},
				Category:    "Deploys",
				Access:      "read",
				ReadOnly:    false,
				Params:      map[string]string{"service": "Filter by service", "commit_hash": "Git commit hash (record)", "environment": "Deployment environment"},
			})
		b.Add("deploys", "Deploy management: history, impact, record", "Deploys", "read", "")
	}

	// --- servers (unified: list_servers + query_metrics + server_health) ---
	if deps.ServerStore != nil && deps.MetricStore != nil {
		gw.Register("servers",
			wrapHandler("servers", tools.ServersHandler(tools.ServersDeps{
				ServerStore: deps.ServerStore,
				MetricStore: deps.MetricStore,
			})),
			GatewayEntry{
				Description: "Server infrastructure monitoring: list, query metrics, health snapshots",
				Actions:     []string{"list", "query", "health"},
				Category:    "Server Metrics",
				Access:      "read",
				ReadOnly:    true,
				Idempotent:  true,
				Params:      map[string]string{"server_id": "Server UUID", "metric_name": "e.g. cpu.usage_percent", "start": "ISO 8601 start time", "end": "ISO 8601 end time"},
			})
		b.Add("servers", "Server infrastructure monitoring: list, query metrics, health snapshots", "Server Metrics", "read", "")
	}

	// --- setup ---
	gw.Register("setup",
		wrapHandler("setup", tools.SetupHandler(tools.SetupDeps{
			LogStore:      deps.LogStore,
			UserStore:     deps.UserStore,
			SettingsStore: deps.SettingsStore,
			DSStore:       deps.DSStore,
		})),
		GatewayEntry{
			Description: "Onboarding assistant: check status, detect framework, get SDK guide, verify data flow",
			Actions:     []string{"status", "detect", "guide", "verify"},
			Category:    "Setup",
			Access:      "read",
			ReadOnly:    true,
			Idempotent:  true,
		})
	b.Add("setup", "Onboarding assistant: check status, detect framework, get SDK guide, verify data flow", "Setup", "read", "")
}

// registerWriteTools adds write/admin tools to the gateway.
func registerWriteTools(gw *Gateway, deps Deps, b *CatalogBuilder) {
	// Dynamic connector tools — registered individually (not through gateway).
	// These are still added to the gateway as pass-through handlers.
	for _, t := range deps.Registry.AllTools() {
		handler := bridgeHandler(t)
		gw.Register(t.Name,
			wrapHandler(t.Name, handler),
			GatewayEntry{
				Description: t.Description,
				Category:    "Connector Queries",
				Access:      "admin",
			})
	}

	// --- admin (settings, users, audit — no notes/session_summary, those are in overview for all users) ---
	gw.Register("admin",
		wrapHandler("admin", tools.AdminHandler(tools.AdminDeps{
			SettingsStore:    deps.SettingsStore,
			UserStore:        deps.UserStore,
			AuditStore:       deps.AuditStore,
			MCPActivityStore: deps.MCPActivityStore,
			Registry:         deps.Registry,
		})),
		GatewayEntry{
			Description: "Admin operations: settings, users, audit, retention",
			Actions:     []string{"update_retention", "users", "update_role", "toggle_active", "delete_user", "audit"},
			Category:    "Admin",
			Access:      "admin",
			ReadOnly:    false,
			Destructive: true,
			Params:      map[string]string{"user_id": "User ID", "role": "admin/member", "entity_type": "query/endpoint/service/healthcheck/error", "summary": "Investigation summary (session_summary)"},
		})
	b.Add("admin", "Admin operations: settings, users, audit, notes, retention, activity, session_summary", "Admin", "admin", "")
}

// handleSessionSummaryFromArgs handles the session_summary action using the
// package-level sessionTracker. This bridges the tools package (which can't
// access the mcp package's sessionTracker) with the actual implementation.
func handleSessionSummaryFromArgs(args map[string]any) (*mcplib.CallToolResult, error) {
	summary, _ := args["summary"].(string)
	rootCause, _ := args["root_cause"].(string)
	fixApplied, _ := args["fix_applied"].(string)
	outcome, _ := args["outcome"].(string)
	primaryService, _ := args["primary_service"].(string)

	if summary == "" {
		return mcplib.NewToolResultError("summary is required"), nil
	}

	if sessionTracker == nil {
		return mcplib.NewToolResultError("session tracking is not enabled"), nil
	}

	var status *store.InvestigationSessionStatus
	if outcome != "" {
		switch outcome {
		case "resolved":
			s := store.InvestigationStatusResolved
			status = &s
		case "unresolved", "partial":
			s := store.InvestigationStatusUnresolved
			status = &s
		default:
			s := store.InvestigationSessionStatus(outcome)
			status = &s
		}
	}

	params := store.UpdateInvestigationSessionParams{
		Summary: &summary,
	}
	if rootCause != "" {
		params.RootCause = &rootCause
	}
	if fixApplied != "" {
		params.FixDescription = &fixApplied
	}
	if status != nil {
		params.Status = status
	}
	if primaryService != "" {
		params.PrimaryService = &primaryService
	}

	sessionTracker.UpdateSession(params)

	resp := map[string]any{
		"status":  "saved",
		"message": "Session summary recorded. This will help future investigations of similar issues.",
	}
	if sessID := sessionTracker.CurrentSessionID(); sessID != "" {
		resp["session_id"] = sessID
	}

	data, _ := json.Marshal(resp)
	return mcplib.NewToolResultText(string(data)), nil
}

// wrapHandler applies activity logging and metrics to a handler.
func wrapHandler(toolName string, handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	handler = wrapWithMetrics(toolName, handler)
	if activityStoreForLogging != nil {
		handler = wrapWithActivityLog(activityStoreForLogging, toolName, handler)
	}
	return handler
}

// ---------------------------------------------------------------------------
// Legacy helpers (still needed for dynamic connector tools)
// ---------------------------------------------------------------------------

// convertTool maps a connector.Tool to an mcp.Tool with the appropriate
// JSON Schema properties derived from the tool's parameter definitions.
func convertTool(t connector.Tool) mcplib.Tool {
	opts := []mcplib.ToolOption{
		mcplib.WithDescription(t.Description),
	}

	for _, p := range t.Params {
		var propOpts []mcplib.PropertyOption
		if p.Required {
			propOpts = append(propOpts, mcplib.Required())
		}

		switch p.Type {
		case "string":
			opts = append(opts, mcplib.WithString(p.Name, propOpts...))
		case "int":
			opts = append(opts, mcplib.WithNumber(p.Name, propOpts...))
		case "bool":
			opts = append(opts, mcplib.WithBoolean(p.Name, propOpts...))
		default:
			opts = append(opts, mcplib.WithString(p.Name, propOpts...))
		}
	}

	return mcplib.NewTool(t.Name, opts...)
}

// bridgeHandler wraps a connector.Tool handler as an MCP ToolHandlerFunc.
func bridgeHandler(t connector.Tool) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		args := request.GetArguments()
		if args == nil {
			args = make(map[string]any)
		}

		result, err := t.Handler(ctx, args)
		if err != nil {
			return mcplib.NewToolResultError(err.Error()), nil
		}

		return mcplib.NewToolResultText(result), nil
	}
}

