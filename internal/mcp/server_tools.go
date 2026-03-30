package mcp

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/domain/logs"
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
		wrapHandler(deps, "connectors", tools.ConnectorsHandler(tools.ConnectorsDeps{
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
		wrapHandler(deps, "database", tools.DatabaseHandler(tools.DatabaseDeps{
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
		if deps.SessionTracker != nil {
			st := deps.SessionTracker // capture for closure
			traceRecorder = func(traceID string) {
				if sess := st.CurrentSession(); sess != nil {
					traceIDs := append([]string{}, sess.TraceIDs...)
					traceIDs = append(traceIDs, traceID)
					st.UpdateSession(store.UpdateInvestigationSessionParams{
						TraceIDs: traceIDs,
					})
				}
			}
		}
		logService := logs.NewService(deps.LogStore)
		gw.Register("logs",
			wrapHandler(deps, "logs", tools.LogsHandler(tools.LogsDeps{
				Logs:                 logService,
				LogStore:             deps.LogStore,
				ErrorGroupStore:      deps.ErrorGroupStore,
				TraceSessionRecorder: traceRecorder,
				Ranker:               rankingServiceAdapter(deps),
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
		if deps.SessionTracker != nil {
			sess = deps.SessionTracker
		}
		if deps.RecurrenceDetector != nil {
			rec = deps.RecurrenceDetector
		}
		gw.Register("errors",
			wrapHandler(deps, "errors", tools.ErrorsHandler(tools.ErrorsDeps{
				ErrorGroupStore:  deps.ErrorGroupStore,
				LogStore:         deps.LogStore,
				ErrorImpactStore: deps.ErrorImpactStore,
				Session:          sess,
				Recurrence:       rec,
				Ranker:           rankingServiceAdapter(deps),
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
			wrapHandler(deps, "healthchecks", tools.HealthchecksHandler(tools.HealthchecksDeps{
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
	if deps.InvestigationSessionStore != nil && deps.SessionTracker != nil {
		st := deps.SessionTracker // capture for closure
		overviewSessionCallback = func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
			return handleSessionSummaryFromArgs(st, args)
		}
	}
	gw.Register("overview",
		wrapHandler(deps, "overview", tools.OverviewHandler(tools.OverviewDeps{
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
			wrapHandler(deps, "watches", tools.WatchesHandler(tools.WatchesDeps{
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
			wrapHandler(deps, "analytics", tools.AnalyticsHandler(tools.AnalyticsDeps{
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
			wrapHandler(deps, "code", tools.CodeHandler(tools.CodeDeps{
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
				Category:   "Code Intelligence",
				Access:     "read",
				ReadOnly:   true,
				Idempotent: true,
				Params:     map[string]string{"service": "Filter by service", "fingerprint": "Error fingerprint", "path": "Source file path", "files": "Array of file paths (risk)"},
			})
		b.Add("code", "Code intelligence, annotations, test generation, and dependency analysis", "Code Intelligence", "read", "")
	}

	// --- deploys ---
	if deps.DeployStore != nil {
		gw.Register("deploys",
			wrapHandler(deps, "deploys", tools.DeploysHandler(tools.DeploysDeps{
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
			wrapHandler(deps, "servers", tools.ServersHandler(tools.ServersDeps{
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
		wrapHandler(deps, "setup", tools.SetupHandler(tools.SetupDeps{
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
			wrapHandler(deps, t.Name, handler),
			GatewayEntry{
				Description: t.Description,
				Category:    "Connector Queries",
				Access:      "admin",
			})
	}

	// --- admin (settings, users, audit — no notes/session_summary, those are in overview for all users) ---
	gw.Register("admin",
		wrapHandler(deps, "admin", tools.AdminHandler(tools.AdminDeps{
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
// provided SessionTracker. This bridges the tools package with the session
// tracking implementation.
func handleSessionSummaryFromArgs(st *SessionTracker, args map[string]any) (*mcp.CallToolResult, error) {
	summary, _ := args["summary"].(string)
	rootCause, _ := args["root_cause"].(string)
	fixApplied, _ := args["fix_applied"].(string)
	outcome, _ := args["outcome"].(string)
	primaryService, _ := args["primary_service"].(string)

	if summary == "" {
		return NewToolResultError("summary is required"), nil
	}

	if st == nil {
		return NewToolResultError("session tracking is not enabled"), nil
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

	st.UpdateSession(params)

	resp := map[string]any{
		"status":  "saved",
		"message": "Session summary recorded. This will help future investigations of similar issues.",
	}
	if sessID := st.CurrentSessionID(); sessID != "" {
		resp["session_id"] = sessID
	}

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}
