package mcp

import (
	"github.com/adham90/opentrace/internal/domain/logs"
	"github.com/adham90/opentrace/internal/mcp/tools"
)

// ---------------------------------------------------------------------------
// Gateway-based tool registration
// ---------------------------------------------------------------------------

// buildGateway creates the Gateway with all tools registered.
// The CatalogBuilder is populated in parallel for the web UI.
func buildGateway(deps Deps, isAdmin bool, b *CatalogBuilder) *Gateway {
	gw := NewGateway()

	registerReadOnlyTools(gw, deps, isAdmin, b)
	if isAdmin {
		registerWriteTools(gw, deps, b)
	}

	return gw
}

// registerReadOnlyTools adds read-only tools to the gateway. isAdmin gates the
// destructive kill_query action of the (otherwise read) database tool.
func registerReadOnlyTools(gw *Gateway, deps Deps, isAdmin bool, b *CatalogBuilder) {
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

	// --- database ---
	gw.Register("database",
		wrapHandler(deps, "database", tools.DatabaseHandler(tools.DatabaseDeps{
			Registry: deps.Registry,
			LogStore: deps.LogStore,
			IsAdmin:  isAdmin,
		})),
		GatewayEntry{
			Description: "Database introspection and management",
			Actions:     []string{"queries", "explain", "tables", "activity", "locks", "connections", "indexes", "schema", "storage", "kill_query", "long_transactions"},
			Category:    "Database",
			Access:      "read",
			ReadOnly:    false,
			Destructive: true,
			Params:      map[string]string{"order_by": "Sort: calls, total_exec_time, mean_exec_time", "query": "SQL query (explain)", "pid": "PID to kill (kill_query)"},
		})
	b.Add("database", "Database introspection and management: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions", "Database Introspection", "read", "database connector")

	// --- logs ---
	if deps.LogStore != nil {
		logService := logs.NewService(deps.LogStore)
		gw.Register("logs",
			wrapHandler(deps, "logs", tools.LogsHandler(tools.LogsDeps{
				Logs:            logService,
				LogStore:        deps.LogStore,
				ErrorGroupStore: deps.ErrorGroupStore,
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
		gw.Register("errors",
			wrapHandler(deps, "errors", tools.ErrorsHandler(tools.ErrorsDeps{
				ErrorGroupStore:  deps.ErrorGroupStore,
				LogStore:         deps.LogStore,
				ErrorImpactStore: deps.ErrorImpactStore,
			})),
			GatewayEntry{
				Description: "Error management: list, detail, investigate, impact, user_errors, ranking, resolve, ignore, reopen, new",
				Actions:     []string{"list", "detail", "investigate", "impact", "user_errors", "ranking", "resolve", "ignore", "reopen", "new"},
				Category:    "Errors",
				Access:      "read",
				ReadOnly:    false,
				Params:      map[string]string{"fingerprint": "Error fingerprint", "service": "Filter by service", "status": "unresolved/resolved/ignored", "limit": "Max results"},
			})
		b.Add("errors", "Manage and investigate errors: list, detail, investigate, impact, user_errors, ranking, resolve, ignore, reopen, new", "Errors", "read", "")
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

	// --- overview (includes agent memory: notes) ---
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
		})),
		GatewayEntry{
			Description: "System overview, triage, diagnosis, incident timeline, and agent memory",
			Actions:     []string{"status", "triage", "diagnose", "timeline", "investigate", "changes", "settings", "notes", "delete_note"},
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
	if deps.CodeEntityStore != nil ||
		deps.AnalyticsStore != nil || deps.ErrorGroupStore != nil {
		gw.Register("code",
			wrapHandler(deps, "code", tools.CodeHandler(tools.CodeDeps{
				CodeEntityStore:  deps.CodeEntityStore,
				ErrorGroupStore:  deps.ErrorGroupStore,
				ErrorImpactStore: deps.ErrorImpactStore,
				AgentNoteStore:   deps.AgentNoteStore,
				AnalyticsStore:   deps.AnalyticsStore,
				LogStore:         deps.LogStore,
			})),
			GatewayEntry{
				Description: "Code intelligence, annotations, test generation, and dependency analysis",
				Actions: []string{
					"risk", "fragile",
					"annotate_file", "annotate_function", "hotspots",
					"gen_context", "gen_suggest",
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

	// --- deep_capture ---
	if deps.DB != nil {
		gw.Register("deep_capture",
			wrapHandler(deps, "deep_capture", tools.DeepCaptureHandler(tools.DeepCaptureDeps{
				DB: deps.DB,
			})),
			GatewayEntry{
				Description: "Deep capture data: request/response details, SQL queries, HTTP calls, emails, audit trail, file operations, PII config, retention policy",
				Actions: []string{
					"request_capture", "sql_captures", "http_captures", "email_captures",
					"audit_trail", "search_audit", "search_sql", "file_captures",
					"get_pii_config", "update_pii_config", "get_retention", "update_retention",
				},
				Category:   "Deep Capture",
				Access:     "read",
				ReadOnly:   false,
				Idempotent: false,
				Params: map[string]string{
					"log_id":         "Log entry ID (for per-request captures)",
					"record_type":    "Record type (audit_trail)",
					"record_id":      "Record ID (audit_trail)",
					"actor_id":       "Actor ID (search_audit)",
					"action":         "Action filter (search_audit)",
					"fingerprint":    "SQL fingerprint (search_sql)",
					"table_name":     "Table name (search_sql)",
					"min_duration_ms": "Minimum SQL duration in ms (search_sql)",
					"last":           "Time window: 1h, 24h, 7d (email_captures, search_audit, search_sql)",
					"limit":          "Max results (search_sql, default 50)",
					"config":         "JSON config object (update_pii_config, update_retention)",
				},
			})
		b.Add("deep_capture", "Deep capture data: request/response, SQL, HTTP, emails, audit, files, PII config, retention", "Deep Capture", "read", "")
	}
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

