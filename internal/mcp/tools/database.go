package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/guardrail"
	"github.com/adham90/opentrace/internal/store"
)

// ---------------------------------------------------------------------------
// SessionTracking — optional callback interface for session tracking
// ---------------------------------------------------------------------------

// SessionTracking allows the consolidated handlers to record actions
// on the current investigation session without depending on the mcp
// package's unexported sessionTracker variable.
type SessionTracking interface {
	TrackExplainedQuery(fingerprint string)
	TrackKilledQuery(pid string)
	TrackRunbookExecution(playbook string)
}

// ---------------------------------------------------------------------------
// Dependencies
// ---------------------------------------------------------------------------

// DatabaseDeps holds all dependencies needed by the database consolidated tool.
type DatabaseDeps struct {
	Registry         *connector.Registry
	QueryMemoryStore store.QueryMemoryStore
	SessionTracking  SessionTracking // optional
}

// RunbookDeps holds all dependencies needed by the runbook tool.
type RunbookDeps struct {
	Registry                 *connector.Registry
	LogStore                 store.LogStore
	RunbookEffectivenessStore store.RunbookEffectivenessStore
	SessionTracking          SessionTracking // optional
}

// ---------------------------------------------------------------------------
// database tool definition
// ---------------------------------------------------------------------------

// DatabaseTool returns the MCP tool definition for the consolidated database tool.
func DatabaseTool() mcp.Tool {
	return mcp.NewTool("database",
		mcp.WithDescription(
			"Consolidated database introspection and management tool. "+
				"Supports multiple actions: queries, explain, tables, activity, locks, "+
				"connections, indexes, schema, storage, kill_query, long_transactions. "+
				"Use the 'action' parameter to select the operation."),
		mcp.WithString("action", mcp.Required(),
			mcp.Description("Action to perform: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions"),
		),
		// queries action params
		mcp.WithString("order_by",
			mcp.Description("(queries) Sort by: calls, total_exec_time (default), mean_exec_time, rows, shared_blks_hit, shared_blks_read"),
		),
		mcp.WithNumber("limit",
			mcp.Description("(queries) Number of queries to return (default: 20, max: 100)"),
		),
		mcp.WithString("filter",
			mcp.Description("(queries) Filter queries containing this text"),
		),
		// explain action params
		mcp.WithString("query",
			mcp.Description("(explain) The SQL SELECT query to analyze"),
		),
		mcp.WithString("format",
			mcp.Description("(explain) Output format: 'text' (default) or 'json'"),
		),
		mcp.WithBoolean("analyze",
			mcp.Description("(explain) Actually execute the query for real timing (default: false). Set to true for EXPLAIN ANALYZE."),
		),
		mcp.WithBoolean("buffers",
			mcp.Description("(explain) Include buffer usage stats (default: true, only with analyze=true)"),
		),
		// tables action params
		mcp.WithString("table_name",
			mcp.Description("(tables/indexes) Filter to a specific table name"),
		),
		// locks action params
		mcp.WithBoolean("blocking_only",
			mcp.Description("(locks) Only show blocking chains (default: true). Set to false to see all held locks."),
		),
		// indexes action params
		mcp.WithBoolean("include_suggestions",
			mcp.Description("(indexes) Include CREATE/DROP INDEX suggestions (default: true)"),
		),
		// schema action params
		mcp.WithString("schema",
			mcp.Description("(schema) Schema name (default: public)"),
		),
		mcp.WithString("table",
			mcp.Description("(schema) Get detailed info for a specific table. Omit for overview of all tables."),
		),
		// kill_query action params
		mcp.WithNumber("pid",
			mcp.Description("(kill_query) Process ID of the backend to cancel"),
		),
		mcp.WithBoolean("force",
			mcp.Description("(kill_query) Use pg_terminate_backend instead of pg_cancel_backend (default: false)"),
		),
		// long_transactions action params
		mcp.WithNumber("min_duration_seconds",
			mcp.Description("(long_transactions) Minimum transaction duration in seconds (default: 30)"),
		),
	)
}

// DatabaseHandler returns the handler for the consolidated database tool.
func DatabaseHandler(deps DatabaseDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		action, _ := args["action"].(string)
		if action == "" {
			return mcp.NewToolResultError("action is required. Available: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions"), nil
		}

		switch action {
		case "queries":
			return handleQueries(ctx, deps, args)
		case "explain":
			return handleExplain(ctx, deps, args)
		case "tables":
			return handleTables(ctx, deps, args)
		case "activity":
			return handleDatabaseActivity(ctx, deps, args)
		case "locks":
			return handleLocks(ctx, deps, args)
		case "connections":
			return handleConnections(ctx, deps, args)
		case "indexes":
			return handleIndexes(ctx, deps, args)
		case "schema":
			return handleSchema(ctx, deps, args)
		case "storage":
			return handleStorage(ctx, deps, args)
		case "kill_query":
			return handleKillQuery(ctx, deps, args)
		case "long_transactions":
			return handleLongTransactions(ctx, deps, args)
		default:
			return mcp.NewToolResultError(fmt.Sprintf("unknown action %q. Available: queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions", action)), nil
		}
	}
}

// ---------------------------------------------------------------------------
// runbook tool definition
// ---------------------------------------------------------------------------

// RunbookTool returns the MCP tool definition for the runbook tool.
func RunbookTool() mcp.Tool {
	return mcp.NewTool("runbook",
		mcp.WithDescription(
			"Run a composite investigation playbook that executes multiple diagnostic queries at once. "+
				"Available playbooks: slow_database, connection_exhaustion, disk_pressure, replication_lag, error_spike. "+
				"Use when you need a comprehensive investigation rather than individual tool calls."),
		mcp.WithString("playbook", mcp.Required(),
			mcp.Description("Playbook to run: slow_database, connection_exhaustion, disk_pressure, replication_lag, error_spike"),
		),
	)
}

// RunbookHandler returns the handler for the runbook tool.
func RunbookHandler(deps RunbookDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		playbook, _ := args["playbook"].(string)
		if playbook == "" {
			return mcp.NewToolResultError("playbook is required. Available: slow_database, connection_exhaustion, disk_pressure, replication_lag, error_spike"), nil
		}

		var result *mcp.CallToolResult
		var err error

		switch playbook {
		case "slow_database":
			result, err = runbookSlowDB(ctx, deps.Registry)
		case "connection_exhaustion":
			result, err = runbookConnExhaustion(ctx, deps.Registry)
		case "disk_pressure":
			result, err = runbookDiskPressure(ctx, deps.Registry)
		case "replication_lag":
			result, err = runbookReplicationLag(ctx, deps.Registry)
		case "error_spike":
			result, err = runbookErrorSpike(ctx, deps.LogStore)
		default:
			return mcp.NewToolResultError(fmt.Sprintf("unknown playbook %q. Available: slow_database, connection_exhaustion, disk_pressure, replication_lag, error_spike", playbook)), nil
		}

		// Track runbook execution on session.
		if err == nil && deps.SessionTracking != nil {
			deps.SessionTracking.TrackRunbookExecution(playbook)
		}
		if err == nil && deps.RunbookEffectivenessStore != nil {
			_ = deps.RunbookEffectivenessStore.RecordExecution(ctx, playbook)
		}

		return result, err
	}
}

// ---------------------------------------------------------------------------
// Helper: get query executor from registry
// ---------------------------------------------------------------------------

func getQueryExecutor(registry *connector.Registry) (connector.QueryExecutor, *mcp.CallToolResult) {
	ds := registry.Get(connector.ConnectorDatabase)
	if ds == nil {
		return nil, mcp.NewToolResultError("No database connector is active. Connect a PostgreSQL data source first.")
	}
	qe, ok := ds.(connector.QueryExecutor)
	if !ok {
		return nil, mcp.NewToolResultError("The active database connector does not support direct queries.")
	}
	return qe, nil
}

// ---------------------------------------------------------------------------
// Action: queries — query stats from pg_stat_statements
// ---------------------------------------------------------------------------

func handleQueries(ctx context.Context, deps DatabaseDeps, args map[string]any) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	orderBy := "total_exec_time"
	if v, ok := args["order_by"].(string); ok && v != "" {
		allowed := map[string]bool{
			"calls": true, "total_exec_time": true, "mean_exec_time": true,
			"rows": true, "shared_blks_hit": true, "shared_blks_read": true,
		}
		if allowed[v] {
			orderBy = v
		}
	}

	limit := 20
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
		if limit > 100 {
			limit = 100
		}
	}

	filter, _ := args["filter"].(string)

	whereClause := ""
	if filter != "" {
		escaped := strings.ReplaceAll(filter, "'", "''")
		whereClause = fmt.Sprintf("WHERE query ILIKE '%%%s%%'", escaped)
	}

	query := fmt.Sprintf(`SELECT
  queryid,
  LEFT(query, 200) AS query_preview,
  calls,
  total_exec_time,
  mean_exec_time,
  rows,
  shared_blks_hit,
  shared_blks_read
FROM pg_stat_statements
%s
ORDER BY %s DESC
LIMIT %d`, whereClause, orderBy, limit)

	result, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "pg_stat_statements") && strings.Contains(errMsg, "does not exist") {
			return mcp.NewToolResultText(
				"pg_stat_statements extension is not enabled.\n\n" +
					"To enable it:\n" +
					"1. Add to postgresql.conf: shared_preload_libraries = 'pg_stat_statements'\n" +
					"2. Restart PostgreSQL\n" +
					"3. Run: CREATE EXTENSION IF NOT EXISTS pg_stat_statements;\n\n" +
					"This extension tracks query execution statistics and is very useful for identifying slow queries."), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("failed to query pg_stat_statements: %v", err)), nil
	}

	if result.RowCount == 0 {
		return mcp.NewToolResultText("No query statistics found. The database may have been recently restarted, or pg_stat_statements may have no recorded queries yet."), nil
	}

	type queryStatsRow struct {
		QueryID        any     `json:"query_id"`
		QueryPreview   string  `json:"query_preview"`
		Calls          any     `json:"calls"`
		TotalExecTime  any     `json:"total_exec_time_ms"`
		MeanExecTime   any     `json:"mean_exec_time_ms"`
		Rows           any     `json:"rows"`
		SharedBlksHit  any     `json:"shared_blks_hit"`
		SharedBlksRead any     `json:"shared_blks_read"`
	}

	rows := make([]queryStatsRow, 0, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) < 8 {
			continue
		}
		rows = append(rows, queryStatsRow{
			QueryID:        row[0],
			QueryPreview:   fmt.Sprintf("%v", row[1]),
			Calls:          row[2],
			TotalExecTime:  row[3],
			MeanExecTime:   row[4],
			Rows:           row[5],
			SharedBlksHit:  row[6],
			SharedBlksRead: row[7],
		})
	}

	resp := map[string]any{
		"order_by":    orderBy,
		"limit":       limit,
		"total_found": len(rows),
		"queries":     rows,
		"hint":        "Use these stats to identify slow queries, high-frequency queries, or queries with poor cache hit ratios. Consider creating watchers for the most impactful ones.",
	}

	// Suggest explain for the top query.
	if len(rows) > 0 {
		queryText := rows[0].QueryPreview
		if queryText != "" {
			why := "Slowest query"
			if orderBy == "calls" {
				why = "Most frequent query"
			}
			WithSuggestions(resp,
				Suggest("database", why+" — see execution plan", map[string]any{
					"action": "explain",
					"query":  queryText,
				}),
			)
		}
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: explain — EXPLAIN ANALYZE a query
// ---------------------------------------------------------------------------

// queryFingerprintRe strips string/numeric literals to create a query fingerprint.
var queryFingerprintRe = regexp.MustCompile(`'[^']*'|"[^"]*"|\b\d+(\.\d+)?\b`)

// normalizeQueryFingerprint creates a stable fingerprint by replacing literals with ?.
func normalizeQueryFingerprint(query string) string {
	return strings.TrimSpace(queryFingerprintRe.ReplaceAllString(query, "?"))
}

func handleExplain(ctx context.Context, deps DatabaseDeps, args map[string]any) (*mcp.CallToolResult, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return mcp.NewToolResultError("query is required for the explain action"), nil
	}

	// Validate that it's a SELECT statement.
	if err := guardrail.ValidateReadOnlyGeneric(query); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("only SELECT queries can be explained: %v", err)), nil
	}

	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	analyze := false
	if v, ok := args["analyze"].(bool); ok {
		analyze = v
	}

	buffers := true
	if v, ok := args["buffers"].(bool); ok {
		buffers = v
	}

	outputFormat := "text"
	if v, ok := args["format"].(string); ok && (v == "json" || v == "text") {
		outputFormat = v
	}

	// Build the EXPLAIN command.
	var explainPrefix string
	if analyze {
		opts := []string{"ANALYZE true"}
		if buffers {
			opts = append(opts, "BUFFERS true")
		}
		if outputFormat == "json" {
			opts = append(opts, "FORMAT JSON")
		}
		explainPrefix = fmt.Sprintf("EXPLAIN (%s) ", strings.Join(opts, ", "))
	} else {
		if outputFormat == "json" {
			explainPrefix = "EXPLAIN (FORMAT JSON) "
		} else {
			explainPrefix = "EXPLAIN "
		}
	}

	explainQuery := explainPrefix + query

	result, err := qe.ExecuteReadQuery(ctx, explainQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("EXPLAIN failed: %v", err)), nil
	}

	// Collect the plan output.
	var planLines []string
	for _, row := range result.Rows {
		if len(row) > 0 {
			planLines = append(planLines, fmt.Sprintf("%v", row[0]))
		}
	}
	planText := strings.Join(planLines, "\n")

	// Build warnings by analyzing the plan text.
	warnings := analyzeExplainPlan(planText)

	resp := map[string]any{
		"query": query,
		"plan":  planText,
	}

	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}

	// Query memory: check for prior investigation of this query.
	fingerprint := normalizeQueryFingerprint(query)
	if deps.QueryMemoryStore != nil && fingerprint != "" {
		qm, qmErr := deps.QueryMemoryStore.Get(ctx, fingerprint)
		if qmErr == nil && qm != nil {
			resp["query_memory"] = map[string]any{
				"investigation_count": qm.InvestigationCount,
				"last_root_cause":     qm.LastRootCause,
				"last_fix":            qm.LastFix,
			}
		}
	}

	// Track fingerprint on session.
	if deps.SessionTracking != nil && fingerprint != "" {
		deps.SessionTracking.TrackExplainedQuery(fingerprint)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// analyzeExplainPlan inspects EXPLAIN output text for common performance issues.
func analyzeExplainPlan(plan string) []string {
	var warnings []string
	lower := strings.ToLower(plan)

	if strings.Contains(lower, "seq scan") {
		warnings = append(warnings, "Sequential scan detected — consider adding an index on the filtered columns")
	}
	if strings.Contains(lower, "sort method: external") || strings.Contains(lower, "sort method: disk") {
		warnings = append(warnings, "Sort spilling to disk — consider increasing work_mem or adding an index to avoid the sort")
	}
	if strings.Contains(lower, "nested loop") && strings.Contains(lower, "rows=0") {
		warnings = append(warnings, "Nested loop with zero rows — the planner's estimates may be off, consider running ANALYZE on the tables")
	}
	if strings.Contains(lower, "hash join") && strings.Contains(lower, "batches:") {
		if !strings.Contains(lower, "batches: 1") {
			warnings = append(warnings, "Hash join using multiple batches (disk spill) — consider increasing work_mem")
		}
	}

	return warnings
}

// ---------------------------------------------------------------------------
// Action: tables — table stats
// ---------------------------------------------------------------------------

func handleTables(ctx context.Context, deps DatabaseDeps, args map[string]any) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	tableName, _ := args["table_name"].(string)

	query := `SELECT
  s.schemaname,
  s.relname AS table_name,
  s.n_live_tup,
  s.n_dead_tup,
  s.seq_scan,
  s.idx_scan,
  s.n_tup_ins,
  s.n_tup_upd,
  s.n_tup_del,
  s.last_vacuum,
  s.last_autovacuum,
  s.last_analyze,
  s.last_autoanalyze,
  COALESCE(io.heap_blks_hit, 0) AS heap_blks_hit,
  COALESCE(io.heap_blks_read, 0) AS heap_blks_read
FROM pg_stat_user_tables s
LEFT JOIN pg_statio_user_tables io
  ON s.relid = io.relid`

	if tableName != "" {
		query += fmt.Sprintf("\nWHERE s.relname = '%s'", sanitizeIdentifier(tableName))
	}

	query += "\nORDER BY s.n_live_tup DESC\nLIMIT 50"

	result, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query table stats: %v", err)), nil
	}

	if result.RowCount == 0 {
		if tableName != "" {
			return mcp.NewToolResultText(fmt.Sprintf("No statistics found for table %q. Verify the table name exists.", tableName)), nil
		}
		return mcp.NewToolResultText("No user tables found in the database."), nil
	}

	type tableStats struct {
		Schema          string `json:"schema"`
		TableName       string `json:"table_name"`
		LiveTuples      any    `json:"live_tuples"`
		DeadTuples      any    `json:"dead_tuples"`
		SeqScans        any    `json:"seq_scans"`
		IndexScans      any    `json:"index_scans"`
		TupInserted     any    `json:"tuples_inserted"`
		TupUpdated      any    `json:"tuples_updated"`
		TupDeleted      any    `json:"tuples_deleted"`
		LastVacuum      any    `json:"last_vacuum"`
		LastAutovacuum  any    `json:"last_autovacuum"`
		LastAnalyze     any    `json:"last_analyze"`
		LastAutoanalyze any    `json:"last_autoanalyze"`
		HeapBlksHit     any    `json:"heap_blks_hit"`
		HeapBlksRead    any    `json:"heap_blks_read"`
	}

	tables := make([]tableStats, 0, len(result.Rows))
	var warnings []string

	for _, row := range result.Rows {
		if len(row) < 15 {
			continue
		}

		ts := tableStats{
			Schema:          fmt.Sprintf("%v", row[0]),
			TableName:       fmt.Sprintf("%v", row[1]),
			LiveTuples:      row[2],
			DeadTuples:      row[3],
			SeqScans:        row[4],
			IndexScans:      row[5],
			TupInserted:     row[6],
			TupUpdated:      row[7],
			TupDeleted:      row[8],
			LastVacuum:      row[9],
			LastAutovacuum:  row[10],
			LastAnalyze:     row[11],
			LastAutoanalyze: row[12],
			HeapBlksHit:     row[13],
			HeapBlksRead:    row[14],
		}
		tables = append(tables, ts)

		tblLabel := fmt.Sprintf("%s.%s", ts.Schema, ts.TableName)

		// Check dead tuple ratio.
		live := toInt64(row[2])
		dead := toInt64(row[3])
		if live > 0 && dead > 0 {
			ratio := float64(dead) / float64(live+dead) * 100
			if ratio > 10 {
				warnings = append(warnings, fmt.Sprintf("%s: %.0f%% dead tuples (%d dead / %d total) — consider VACUUM", tblLabel, ratio, dead, live+dead))
			}
		}

		// Check index usage.
		seqScans := toInt64(row[4])
		idxScans := toInt64(row[5])
		if seqScans+idxScans > 100 && live > 10000 {
			if idxScans == 0 || (seqScans > 0 && float64(idxScans)/float64(seqScans+idxScans)*100 < 50) {
				warnings = append(warnings, fmt.Sprintf("%s: low index usage — %d seq scans vs %d index scans on %d rows", tblLabel, seqScans, idxScans, live))
			}
		}

		// Check cache hit ratio.
		hit := toInt64(row[13])
		read := toInt64(row[14])
		if hit+read > 100 {
			cacheRatio := float64(hit) / float64(hit+read) * 100
			if cacheRatio < 90 {
				warnings = append(warnings, fmt.Sprintf("%s: low cache hit ratio (%.0f%%) — may need more shared_buffers", tblLabel, cacheRatio))
			}
		}

		// Check stale vacuum.
		if live > 10000 && row[9] == nil && row[10] == nil {
			warnings = append(warnings, fmt.Sprintf("%s: never vacuumed with %d live tuples", tblLabel, live))
		}
	}

	resp := map[string]any{
		"total_tables": len(tables),
		"tables":       tables,
		"warnings":     warnings,
		"hint":         "Tables with high dead tuple ratios, low index usage, or low cache hit ratios are good candidates for watchers.",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: activity — current connections/activity
// ---------------------------------------------------------------------------

func handleDatabaseActivity(ctx context.Context, deps DatabaseDeps, args map[string]any) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	// 1. Connection summary by state.
	summaryQuery := `SELECT
  state,
  COALESCE(application_name, '') AS app_name,
  count(*) AS count
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
GROUP BY state, application_name
ORDER BY count DESC`

	summaryResult, err := qe.ExecuteReadQuery(ctx, summaryQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query connection summary: %v", err)), nil
	}

	type connSummary struct {
		State   string `json:"state"`
		AppName string `json:"app_name"`
		Count   any    `json:"count"`
	}
	summaries := make([]connSummary, 0, len(summaryResult.Rows))
	for _, row := range summaryResult.Rows {
		if len(row) < 3 {
			continue
		}
		summaries = append(summaries, connSummary{
			State:   fmt.Sprintf("%v", row[0]),
			AppName: fmt.Sprintf("%v", row[1]),
			Count:   row[2],
		})
	}

	// 2. Long-running queries (> 10 seconds).
	longRunningQuery := `SELECT
  pid,
  COALESCE(application_name, '') AS app_name,
  state,
  EXTRACT(EPOCH FROM (now() - query_start))::int AS duration_seconds,
  LEFT(query, 200) AS query_preview
FROM pg_stat_activity
WHERE state = 'active'
  AND pid <> pg_backend_pid()
  AND query_start < now() - interval '10 seconds'
ORDER BY query_start ASC
LIMIT 20`

	longResult, err := qe.ExecuteReadQuery(ctx, longRunningQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query long-running queries: %v", err)), nil
	}

	type longQuery struct {
		PID             any    `json:"pid"`
		AppName         string `json:"app_name"`
		State           string `json:"state"`
		DurationSeconds any    `json:"duration_seconds"`
		QueryPreview    string `json:"query_preview"`
	}
	longQueries := make([]longQuery, 0, len(longResult.Rows))
	for _, row := range longResult.Rows {
		if len(row) < 5 {
			continue
		}
		longQueries = append(longQueries, longQuery{
			PID:             row[0],
			AppName:         fmt.Sprintf("%v", row[1]),
			State:           fmt.Sprintf("%v", row[2]),
			DurationSeconds: row[3],
			QueryPreview:    fmt.Sprintf("%v", row[4]),
		})
	}

	// 3. Idle-in-transaction sessions (> 1 minute).
	idleQuery := `SELECT
  pid,
  COALESCE(application_name, '') AS app_name,
  EXTRACT(EPOCH FROM (now() - state_change))::int AS idle_seconds,
  LEFT(query, 200) AS last_query
FROM pg_stat_activity
WHERE state = 'idle in transaction'
  AND pid <> pg_backend_pid()
  AND state_change < now() - interval '1 minute'
ORDER BY state_change ASC
LIMIT 20`

	idleResult, err := qe.ExecuteReadQuery(ctx, idleQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query idle-in-transaction sessions: %v", err)), nil
	}

	type idleSession struct {
		PID         any    `json:"pid"`
		AppName     string `json:"app_name"`
		IdleSeconds any    `json:"idle_seconds"`
		LastQuery   string `json:"last_query"`
	}
	idleSessions := make([]idleSession, 0, len(idleResult.Rows))
	for _, row := range idleResult.Rows {
		if len(row) < 4 {
			continue
		}
		idleSessions = append(idleSessions, idleSession{
			PID:         row[0],
			AppName:     fmt.Sprintf("%v", row[1]),
			IdleSeconds: row[2],
			LastQuery:   fmt.Sprintf("%v", row[3]),
		})
	}

	// 4. Max connections for utilization.
	maxConnResult, err := qe.ExecuteReadQuery(ctx, "SELECT current_setting('max_connections')")
	var maxConns int
	var totalConns int
	if err == nil && maxConnResult.RowCount > 0 && len(maxConnResult.Rows[0]) > 0 {
		maxConns, _ = strconv.Atoi(fmt.Sprintf("%v", maxConnResult.Rows[0][0]))
	}

	// Total current connections.
	totalResult, err := qe.ExecuteReadQuery(ctx, "SELECT count(*) FROM pg_stat_activity")
	if err == nil && totalResult.RowCount > 0 && len(totalResult.Rows[0]) > 0 {
		if v, vErr := strconv.Atoi(fmt.Sprintf("%v", totalResult.Rows[0][0])); vErr == nil {
			totalConns = v
		}
	}

	// Build warnings.
	var warnings []string
	if maxConns > 0 {
		utilization := float64(totalConns) / float64(maxConns) * 100
		if utilization > 80 {
			warnings = append(warnings, fmt.Sprintf("High connection utilization: %d/%d (%.0f%%)", totalConns, maxConns, utilization))
		}
	}
	if len(longQueries) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d long-running queries (>10s) detected", len(longQueries)))
	}
	if len(idleSessions) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d idle-in-transaction sessions (>1min) detected", len(idleSessions)))
	}

	resp := map[string]any{
		"total_connections":      totalConns,
		"connection_summary":     summaries,
		"long_running_queries":   longQueries,
		"idle_in_transaction":    idleSessions,
		"warnings":               warnings,
	}
	if maxConns > 0 {
		resp["max_connections"] = maxConns
		resp["utilization_percent"] = float64(totalConns) / float64(maxConns) * 100
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: locks — lock contention
// ---------------------------------------------------------------------------

func handleLocks(ctx context.Context, deps DatabaseDeps, args map[string]any) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	blockingOnly := true
	if v, ok := args["blocking_only"].(bool); ok {
		blockingOnly = v
	}

	if blockingOnly {
		return fetchBlockingChains(ctx, qe)
	}
	return fetchAllLocks(ctx, qe)
}

func fetchBlockingChains(ctx context.Context, qe connector.QueryExecutor) (*mcp.CallToolResult, error) {
	query := `SELECT
  blocking_activity.pid AS blocking_pid,
  COALESCE(blocking_activity.application_name, '') AS blocking_app,
  blocking_activity.state AS blocking_state,
  LEFT(blocking_activity.query, 300) AS blocking_query,
  EXTRACT(EPOCH FROM (now() - blocking_activity.query_start))::int AS blocking_duration_seconds,
  blocked_activity.pid AS blocked_pid,
  COALESCE(blocked_activity.application_name, '') AS blocked_app,
  blocked_activity.state AS blocked_state,
  LEFT(blocked_activity.query, 300) AS blocked_query,
  EXTRACT(EPOCH FROM (now() - blocked_activity.query_start))::int AS blocked_duration_seconds,
  blocked_locks.locktype AS lock_type,
  COALESCE(blocked_locks.relation::regclass::text, '') AS relation
FROM pg_catalog.pg_locks blocked_locks
JOIN pg_catalog.pg_stat_activity blocked_activity
  ON blocked_activity.pid = blocked_locks.pid
JOIN pg_catalog.pg_locks blocking_locks
  ON blocking_locks.locktype = blocked_locks.locktype
  AND blocking_locks.database IS NOT DISTINCT FROM blocked_locks.database
  AND blocking_locks.relation IS NOT DISTINCT FROM blocked_locks.relation
  AND blocking_locks.page IS NOT DISTINCT FROM blocked_locks.page
  AND blocking_locks.tuple IS NOT DISTINCT FROM blocked_locks.tuple
  AND blocking_locks.virtualxid IS NOT DISTINCT FROM blocked_locks.virtualxid
  AND blocking_locks.transactionid IS NOT DISTINCT FROM blocked_locks.transactionid
  AND blocking_locks.classid IS NOT DISTINCT FROM blocked_locks.classid
  AND blocking_locks.objid IS NOT DISTINCT FROM blocked_locks.objid
  AND blocking_locks.objsubid IS NOT DISTINCT FROM blocked_locks.objsubid
  AND blocking_locks.pid != blocked_locks.pid
JOIN pg_catalog.pg_stat_activity blocking_activity
  ON blocking_activity.pid = blocking_locks.pid
WHERE NOT blocked_locks.granted
ORDER BY blocking_duration_seconds DESC
LIMIT 50`

	result, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query lock contention: %v", err)), nil
	}

	type lockEntry struct {
		BlockingPID      any    `json:"blocking_pid"`
		BlockingApp      string `json:"blocking_app"`
		BlockingState    string `json:"blocking_state"`
		BlockingQuery    string `json:"blocking_query"`
		BlockingDuration any    `json:"blocking_duration_seconds"`
		BlockedPID       any    `json:"blocked_pid"`
		BlockedApp       string `json:"blocked_app"`
		BlockedState     string `json:"blocked_state"`
		BlockedQuery     string `json:"blocked_query"`
		BlockedDuration  any    `json:"blocked_duration_seconds"`
		LockType         string `json:"lock_type"`
		Relation         string `json:"relation,omitempty"`
	}

	entries := make([]lockEntry, 0, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) < 12 {
			continue
		}
		entries = append(entries, lockEntry{
			BlockingPID:      row[0],
			BlockingApp:      fmt.Sprintf("%v", row[1]),
			BlockingState:    fmt.Sprintf("%v", row[2]),
			BlockingQuery:    fmt.Sprintf("%v", row[3]),
			BlockingDuration: row[4],
			BlockedPID:       row[5],
			BlockedApp:       fmt.Sprintf("%v", row[6]),
			BlockedState:     fmt.Sprintf("%v", row[7]),
			BlockedQuery:     fmt.Sprintf("%v", row[8]),
			BlockedDuration:  row[9],
			LockType:         fmt.Sprintf("%v", row[10]),
			Relation:         fmt.Sprintf("%v", row[11]),
		})
	}

	var warnings []string
	for _, e := range entries {
		if e.BlockingState == "idle in transaction" {
			warnings = append(warnings,
				fmt.Sprintf("PID %v is idle in transaction while blocking PID %v", e.BlockingPID, e.BlockedPID))
		}
	}

	resp := map[string]any{
		"blocking_chains": entries,
		"total_chains":    len(entries),
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	if len(entries) == 0 {
		resp["message"] = "No blocking lock chains found — the database has no lock contention."
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func fetchAllLocks(ctx context.Context, qe connector.QueryExecutor) (*mcp.CallToolResult, error) {
	query := `SELECT
  l.locktype,
  COALESCE(l.relation::regclass::text, '') AS relation,
  l.mode,
  l.granted,
  l.pid,
  COALESCE(a.application_name, '') AS app_name,
  a.state,
  LEFT(a.query, 200) AS query_preview
FROM pg_catalog.pg_locks l
JOIN pg_catalog.pg_stat_activity a ON a.pid = l.pid
WHERE l.pid <> pg_backend_pid()
ORDER BY l.relation, l.mode
LIMIT 100`

	result, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query locks: %v", err)), nil
	}

	type lockInfo struct {
		LockType     string `json:"lock_type"`
		Relation     string `json:"relation,omitempty"`
		Mode         string `json:"mode"`
		Granted      any    `json:"granted"`
		PID          any    `json:"pid"`
		AppName      string `json:"app_name"`
		State        string `json:"state"`
		QueryPreview string `json:"query_preview"`
	}

	locks := make([]lockInfo, 0, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) < 8 {
			continue
		}
		locks = append(locks, lockInfo{
			LockType:     fmt.Sprintf("%v", row[0]),
			Relation:     fmt.Sprintf("%v", row[1]),
			Mode:         fmt.Sprintf("%v", row[2]),
			Granted:      row[3],
			PID:          row[4],
			AppName:      fmt.Sprintf("%v", row[5]),
			State:        fmt.Sprintf("%v", row[6]),
			QueryPreview: fmt.Sprintf("%v", row[7]),
		})
	}

	resp := map[string]any{
		"locks":       locks,
		"total_locks": len(locks),
	}
	if len(locks) == 0 {
		resp["message"] = "No locks currently held."
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: connections — pool stats + replication
// ---------------------------------------------------------------------------

func handleConnections(ctx context.Context, deps DatabaseDeps, args map[string]any) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	resp := map[string]any{}

	// ---------- Pool stats ----------

	// Get max connections setting.
	maxConnResult, err := qe.ExecuteReadQuery(ctx, `SELECT current_setting('max_connections')`)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query max_connections: %v", err)), nil
	}
	maxConn := 100
	if len(maxConnResult.Rows) > 0 && len(maxConnResult.Rows[0]) > 0 {
		if v, err := fmt.Sscanf(toString(maxConnResult.Rows[0][0]), "%d", &maxConn); v == 0 || err != nil {
			maxConn = 100
		}
	}

	// Get connection summary.
	summaryQuery := `SELECT
		count(*) AS total,
		count(*) FILTER (WHERE state = 'active') AS active,
		count(*) FILTER (WHERE state = 'idle') AS idle,
		count(*) FILTER (WHERE state = 'idle in transaction') AS idle_in_tx,
		count(*) FILTER (WHERE wait_event_type = 'Lock') AS waiting
	FROM pg_stat_activity
	WHERE backend_type = 'client backend'`

	summaryResult, err := qe.ExecuteReadQuery(ctx, summaryQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query connection stats: %v", err)), nil
	}

	var total, active, idle, idleInTx, waiting int64
	if len(summaryResult.Rows) > 0 {
		row := summaryResult.Rows[0]
		if len(row) >= 5 {
			total = toInt64(row[0])
			active = toInt64(row[1])
			idle = toInt64(row[2])
			idleInTx = toInt64(row[3])
			waiting = toInt64(row[4])
		}
	}

	utilPct := float64(0)
	if maxConn > 0 {
		utilPct = float64(total) / float64(maxConn) * 100
	}

	// Get per-application breakdown.
	appQuery := `SELECT
		COALESCE(application_name, '') AS app,
		count(*) AS connections,
		count(*) FILTER (WHERE state = 'active') AS active,
		count(*) FILTER (WHERE state = 'idle') AS idle
	FROM pg_stat_activity
	WHERE backend_type = 'client backend'
	GROUP BY application_name
	ORDER BY count(*) DESC`

	appResult, err := qe.ExecuteReadQuery(ctx, appQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query application stats: %v", err)), nil
	}

	var byApp []map[string]any
	for _, row := range appResult.Rows {
		if len(row) < 4 {
			continue
		}
		byApp = append(byApp, map[string]any{
			"application_name": toString(row[0]),
			"connections":      toInt64(row[1]),
			"active":           toInt64(row[2]),
			"idle":             toInt64(row[3]),
		})
	}

	// Generate pool warnings.
	var poolWarnings []string
	if utilPct > 80 {
		poolWarnings = append(poolWarnings, fmt.Sprintf("Pool utilization at %.0f%% — approaching saturation", utilPct))
	}
	if waiting > 0 {
		poolWarnings = append(poolWarnings, fmt.Sprintf("%d queries waiting for a connection", waiting))
	}
	if idleInTx > 5 {
		poolWarnings = append(poolWarnings, fmt.Sprintf("%d idle-in-transaction connections — these hold locks and waste pool slots", idleInTx))
	}

	pool := map[string]any{
		"max_connections":     maxConn,
		"current_connections": total,
		"active_connections":  active,
		"idle_connections":    idle,
		"idle_in_transaction": idleInTx,
		"utilization_pct":     round2(utilPct),
		"waiting_queries":     waiting,
	}

	connectorInfo := map[string]any{
		"pool":           pool,
		"by_application": byApp,
	}
	if len(poolWarnings) > 0 {
		connectorInfo["warnings"] = poolWarnings
	}

	resp["pool_stats"] = connectorInfo

	// ---------- Replication status ----------

	var replWarnings []string

	// Check if primary or replica.
	roleResult, err := qe.ExecuteReadQuery(ctx, "SELECT pg_is_in_recovery()")
	if err != nil {
		// Non-fatal — just skip replication info.
		resp["replication"] = map[string]any{"error": fmt.Sprintf("failed to check server role: %v", err)}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}

	isReplica := false
	if len(roleResult.Rows) > 0 && len(roleResult.Rows[0]) > 0 {
		if v, ok := roleResult.Rows[0][0].(bool); ok {
			isReplica = v
		}
	}

	repl := map[string]any{}
	if isReplica {
		repl["role"] = "replica"
	} else {
		repl["role"] = "primary"
	}

	// Replication slots.
	slotQuery := `SELECT
		slot_name,
		slot_type,
		active,
		COALESCE(restart_lsn::text, '') AS restart_lsn,
		COALESCE(confirmed_flush_lsn::text, '') AS confirmed_flush_lsn,
		COALESCE(wal_status, 'unknown') AS wal_status
	FROM pg_replication_slots
	ORDER BY slot_name`

	slotResult, err := qe.ExecuteReadQuery(ctx, slotQuery)
	if err == nil && slotResult.RowCount > 0 {
		var slots []map[string]any
		for _, row := range slotResult.Rows {
			if len(row) < 6 {
				continue
			}
			slotActive := false
			if v, ok := row[2].(bool); ok {
				slotActive = v
			}
			slot := map[string]any{
				"slot_name":           toString(row[0]),
				"slot_type":           toString(row[1]),
				"active":              slotActive,
				"restart_lsn":         toString(row[3]),
				"confirmed_flush_lsn": toString(row[4]),
				"wal_status":          toString(row[5]),
			}
			slots = append(slots, slot)

			if !slotActive {
				replWarnings = append(replWarnings, fmt.Sprintf("Replication slot '%s' is inactive — this can cause WAL retention bloat", toString(row[0])))
			}
			walStatus := toString(row[5])
			if walStatus == "lost" {
				replWarnings = append(replWarnings, fmt.Sprintf("Replication slot '%s' has WAL status 'lost' — the replica using this slot will need to be re-initialized", toString(row[0])))
			}
		}
		repl["replication_slots"] = slots
	} else {
		repl["replication_slots"] = []any{}
	}

	// Connected replicas (on primary).
	statQuery := `SELECT
		client_addr,
		state,
		COALESCE(application_name, '') AS application_name,
		sent_lsn::text AS sent_lsn,
		write_lsn::text AS write_lsn,
		flush_lsn::text AS flush_lsn,
		replay_lsn::text AS replay_lsn,
		EXTRACT(EPOCH FROM write_lag)::int AS write_lag_seconds,
		EXTRACT(EPOCH FROM flush_lag)::int AS flush_lag_seconds,
		EXTRACT(EPOCH FROM replay_lag)::int AS replay_lag_seconds
	FROM pg_stat_replication
	ORDER BY client_addr`

	statResult, err := qe.ExecuteReadQuery(ctx, statQuery)
	if err == nil && statResult.RowCount > 0 {
		var replicas []map[string]any
		for _, row := range statResult.Rows {
			if len(row) < 10 {
				continue
			}
			replayLag := toInt64(row[9])
			replica := map[string]any{
				"client_addr":        toString(row[0]),
				"state":              toString(row[1]),
				"application_name":   toString(row[2]),
				"sent_lsn":           toString(row[3]),
				"write_lsn":          toString(row[4]),
				"flush_lsn":          toString(row[5]),
				"replay_lsn":         toString(row[6]),
				"write_lag_seconds":  toInt64(row[7]),
				"flush_lag_seconds":  toInt64(row[8]),
				"replay_lag_seconds": replayLag,
			}
			replicas = append(replicas, replica)

			if replayLag > 30 {
				replWarnings = append(replWarnings, fmt.Sprintf("Replica %s has replay lag of %ds — investigate if the replica is overloaded or network is slow", toString(row[0]), replayLag))
			}
		}
		repl["connected_replicas"] = replicas
	} else {
		repl["connected_replicas"] = []any{}
	}

	// If replica, show lag from primary.
	if isReplica {
		lagQuery := `SELECT
			CASE WHEN pg_last_wal_receive_lsn() IS NOT NULL AND pg_last_wal_replay_lsn() IS NOT NULL
				THEN pg_wal_lsn_diff(pg_last_wal_receive_lsn(), pg_last_wal_replay_lsn())
				ELSE 0
			END AS replay_lag_bytes,
			COALESCE(pg_last_wal_receive_lsn()::text, 'unknown') AS last_received_lsn,
			COALESCE(pg_last_wal_replay_lsn()::text, 'unknown') AS last_replayed_lsn,
			CASE WHEN pg_last_xact_replay_timestamp() IS NOT NULL
				THEN EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))::int
				ELSE NULL
			END AS seconds_behind_primary`

		lagResult, err := qe.ExecuteReadQuery(ctx, lagQuery)
		if err == nil && len(lagResult.Rows) > 0 && len(lagResult.Rows[0]) >= 4 {
			row := lagResult.Rows[0]
			lagBytes := toInt64(row[0])
			secondsBehind := toInt64(row[3])

			repl["replica_status"] = map[string]any{
				"replay_lag_bytes":       lagBytes,
				"last_received_lsn":      toString(row[1]),
				"last_replayed_lsn":      toString(row[2]),
				"seconds_behind_primary": secondsBehind,
			}

			if secondsBehind > 60 {
				replWarnings = append(replWarnings, fmt.Sprintf("This replica is %d seconds behind the primary", secondsBehind))
			}
			if lagBytes > 100*1024*1024 {
				replWarnings = append(replWarnings, fmt.Sprintf("Replay lag is %d bytes — the replica may be struggling to keep up", lagBytes))
			}
		}
	}

	// WAL archival status.
	archiveQuery := `SELECT
		archived_count,
		last_archived_wal,
		last_archived_time::text,
		failed_count,
		COALESCE(last_failed_wal, '') AS last_failed_wal,
		COALESCE(last_failed_time::text, '') AS last_failed_time
	FROM pg_stat_archiver`

	archiveResult, err := qe.ExecuteReadQuery(ctx, archiveQuery)
	if err == nil && len(archiveResult.Rows) > 0 && len(archiveResult.Rows[0]) >= 6 {
		row := archiveResult.Rows[0]
		failedCount := toInt64(row[3])

		archive := map[string]any{
			"archived_count":     toInt64(row[0]),
			"last_archived_wal":  toString(row[1]),
			"last_archived_time": toString(row[2]),
			"failed_count":       failedCount,
		}
		if toString(row[4]) != "" {
			archive["last_failed_wal"] = toString(row[4])
			archive["last_failed_time"] = toString(row[5])
		}
		repl["wal_archival"] = archive

		if failedCount > 0 {
			replWarnings = append(replWarnings, fmt.Sprintf("WAL archiver has %d failed archives — check archive_command configuration and disk space", failedCount))
		}
	}

	if len(replWarnings) > 0 {
		repl["warnings"] = replWarnings
	}

	resp["replication"] = repl

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: indexes — index analysis + bloat
// ---------------------------------------------------------------------------

func handleIndexes(ctx context.Context, deps DatabaseDeps, args map[string]any) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	tableName, _ := args["table_name"].(string)
	includeSuggestions := true
	if v, ok := args["include_suggestions"].(bool); ok {
		includeSuggestions = v
	}

	resp := map[string]any{}

	// --- Index analysis ---

	unused, err := fetchUnusedIndexes(ctx, qe, tableName, includeSuggestions)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query unused indexes: %v", err)), nil
	}
	resp["unused_indexes"] = unused

	missing, err := fetchMissingIndexes(ctx, qe, tableName, includeSuggestions)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query missing indexes: %v", err)), nil
	}
	resp["missing_indexes"] = missing

	duplicates, err := fetchDuplicateIndexes(ctx, qe, tableName, includeSuggestions)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query duplicate indexes: %v", err)), nil
	}
	resp["duplicate_indexes"] = duplicates

	bloatedIdx, err := fetchBloatedIndexes(ctx, qe, tableName, includeSuggestions)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query bloated indexes: %v", err)), nil
	}
	resp["bloated_indexes"] = bloatedIdx

	totalUnusedSize := int64(0)
	for _, u := range unused {
		if sz, ok := u["size_bytes"].(int64); ok {
			totalUnusedSize += sz
		}
	}

	// --- Bloat estimate ---

	bloatQuery := `SELECT
		schemaname || '.' || relname AS table_name,
		pg_size_pretty(pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname))) AS total_size,
		pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) AS total_bytes,
		n_live_tup,
		n_dead_tup,
		CASE WHEN n_live_tup + n_dead_tup > 0
			THEN ROUND(100.0 * n_dead_tup / (n_live_tup + n_dead_tup), 2)
			ELSE 0
		END AS dead_pct,
		CASE WHEN n_live_tup + n_dead_tup > 0 AND pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) > 0
			THEN pg_size_pretty(
				(pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) *
				 n_dead_tup / GREATEST(n_live_tup + n_dead_tup, 1))::bigint
			)
			ELSE '0 bytes'
		END AS estimated_bloat,
		CASE WHEN n_live_tup + n_dead_tup > 0 AND pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) > 0
			THEN (pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) *
				 n_dead_tup / GREATEST(n_live_tup + n_dead_tup, 1))::bigint
			ELSE 0
		END AS estimated_bloat_bytes,
		last_vacuum,
		last_autovacuum
	FROM pg_stat_user_tables
	WHERE n_dead_tup > 0 OR pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) > 1048576
	ORDER BY (pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname)) *
			  n_dead_tup / GREATEST(n_live_tup + n_dead_tup, 1)) DESC
	LIMIT 30`

	bloatResult, err := qe.ExecuteReadQuery(ctx, bloatQuery)
	var bloatTables []map[string]any
	var totalBloatBytes float64
	var bloatRecommendations []string

	if err == nil && bloatResult.RowCount > 0 {
		bloatTables = make([]map[string]any, 0, len(bloatResult.Rows))
		for _, row := range bloatResult.Rows {
			if len(row) < 10 {
				continue
			}
			t := map[string]any{
				"table":                 row[0],
				"total_size":            row[1],
				"total_bytes":           row[2],
				"live_tuples":           row[3],
				"dead_tuples":           row[4],
				"dead_pct":              row[5],
				"estimated_bloat":       row[6],
				"estimated_bloat_bytes": row[7],
				"last_vacuum":           row[8],
				"last_autovacuum":       row[9],
			}
			bloatTables = append(bloatTables, t)

			name := fmt.Sprintf("%v", row[0])
			deadPct, _ := toFloat64(row[5])
			bloatBytes, _ := toFloat64(row[7])
			totalBloatBytes += bloatBytes

			if deadPct > 30 {
				bloatRecommendations = append(bloatRecommendations, fmt.Sprintf("VACUUM FULL %s — %.0f%% dead tuples, would reclaim ~%v", name, deadPct, row[6]))
			} else if deadPct > 10 {
				bloatRecommendations = append(bloatRecommendations, fmt.Sprintf("VACUUM ANALYZE %s — %.0f%% dead tuples, estimated bloat: %v", name, deadPct, row[6]))
			}
		}
	}

	resp["summary"] = map[string]any{
		"unused":                  len(unused),
		"possibly_missing":        len(missing),
		"duplicates":              len(duplicates),
		"bloated_indexes":         len(bloatedIdx),
		"total_unused_size_human": humanSize(totalUnusedSize),
	}

	if len(bloatTables) > 0 {
		resp["bloat_estimate"] = map[string]any{
			"tables":                bloatTables,
			"total_estimated_bloat": fmt.Sprintf("%.0f bytes", totalBloatBytes),
			"recommendations":      bloatRecommendations,
		}
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func fetchUnusedIndexes(ctx context.Context, qe connector.QueryExecutor, tableName string, includeSuggestions bool) ([]map[string]any, error) {
	query := `SELECT
		schemaname,
		relname AS table_name,
		indexrelname AS index_name,
		pg_relation_size(i.indexrelid) AS size_bytes,
		idx_scan
	FROM pg_stat_user_indexes i
	JOIN pg_index pi ON i.indexrelid = pi.indexrelid
	WHERE idx_scan = 0
	  AND NOT pi.indisunique
	  AND NOT pi.indisprimary`

	if tableName != "" {
		query += fmt.Sprintf(` AND relname = '%s'`, sanitizeIdentifier(tableName))
	}
	query += ` ORDER BY pg_relation_size(i.indexrelid) DESC`

	qr, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, row := range qr.Rows {
		if len(row) < 5 {
			continue
		}
		schema := toString(row[0])
		table := toString(row[1])
		indexName := toString(row[2])
		sizeBytes := toInt64(row[3])
		idxScans := toInt64(row[4])

		entry := map[string]any{
			"schema":      schema,
			"table":       table,
			"index_name":  indexName,
			"size_bytes":  sizeBytes,
			"size_human":  humanSize(sizeBytes),
			"index_scans": idxScans,
		}
		if includeSuggestions {
			entry["suggestion"] = fmt.Sprintf("DROP INDEX CONCURRENTLY %s.%s; -- saves %s, 0 scans since stats reset", schema, indexName, humanSize(sizeBytes))
		}
		result = append(result, entry)
	}
	return result, nil
}

func fetchMissingIndexes(ctx context.Context, qe connector.QueryExecutor, tableName string, includeSuggestions bool) ([]map[string]any, error) {
	query := `SELECT
		schemaname,
		relname AS table_name,
		seq_scan,
		seq_tup_read,
		idx_scan,
		n_live_tup
	FROM pg_stat_user_tables
	WHERE n_live_tup > 10000
	  AND seq_scan > COALESCE(idx_scan, 0) * 10`

	if tableName != "" {
		query += fmt.Sprintf(` AND relname = '%s'`, sanitizeIdentifier(tableName))
	}
	query += ` ORDER BY seq_scan DESC`

	qr, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, row := range qr.Rows {
		if len(row) < 6 {
			continue
		}
		schema := toString(row[0])
		table := toString(row[1])
		seqScan := toInt64(row[2])
		seqTupRead := toInt64(row[3])
		idxScan := toInt64(row[4])
		liveTup := toInt64(row[5])

		entry := map[string]any{
			"schema":       schema,
			"table":        table,
			"seq_scans":    seqScan,
			"seq_tup_read": seqTupRead,
			"idx_scans":    idxScan,
			"live_tuples":  liveTup,
		}
		if includeSuggestions {
			entry["suggestion"] = fmt.Sprintf("Table '%s' has %d sequential scans vs %d index scans — likely missing an index on commonly filtered columns", table, seqScan, idxScan)
		}
		result = append(result, entry)
	}
	return result, nil
}

func fetchDuplicateIndexes(ctx context.Context, qe connector.QueryExecutor, tableName string, includeSuggestions bool) ([]map[string]any, error) {
	query := `SELECT
		tablename,
		array_agg(indexname ORDER BY indexname) AS indexes,
		regexp_replace(indexdef, 'CREATE (UNIQUE )?INDEX .+ USING ', '') AS indexdef_normalized
	FROM (
		SELECT
			tablename,
			indexname,
			indexdef
		FROM pg_indexes
		WHERE schemaname = 'public'`

	if tableName != "" {
		query += fmt.Sprintf(` AND tablename = '%s'`, sanitizeIdentifier(tableName))
	}

	query += `
	) sub
	GROUP BY tablename, indexdef_normalized
	HAVING count(*) > 1
	ORDER BY tablename`

	qr, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, row := range qr.Rows {
		if len(row) < 2 {
			continue
		}
		table := toString(row[0])
		indexesRaw := toString(row[1])
		indexes := parsePostgresArray(indexesRaw)

		entry := map[string]any{
			"table":   table,
			"indexes": indexes,
		}
		if includeSuggestions {
			entry["suggestion"] = fmt.Sprintf("Indexes %s cover the same definition — consider dropping one", strings.Join(indexes, " and "))
		}
		result = append(result, entry)
	}
	return result, nil
}

func fetchBloatedIndexes(ctx context.Context, qe connector.QueryExecutor, tableName string, includeSuggestions bool) ([]map[string]any, error) {
	query := `SELECT
		s.schemaname,
		s.relname AS table_name,
		s.indexrelname AS index_name,
		pg_relation_size(s.relid) AS table_size,
		pg_relation_size(s.indexrelid) AS index_size
	FROM pg_stat_user_indexes s
	WHERE pg_relation_size(s.indexrelid) > pg_relation_size(s.relid)
	  AND pg_relation_size(s.relid) > 0`

	if tableName != "" {
		query += fmt.Sprintf(` AND s.relname = '%s'`, sanitizeIdentifier(tableName))
	}
	query += ` ORDER BY pg_relation_size(s.indexrelid) DESC`

	qr, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, row := range qr.Rows {
		if len(row) < 5 {
			continue
		}
		schema := toString(row[0])
		table := toString(row[1])
		indexName := toString(row[2])
		tableSize := toInt64(row[3])
		indexSize := toInt64(row[4])

		ratio := float64(indexSize) / float64(tableSize)
		entry := map[string]any{
			"schema":           schema,
			"table":            table,
			"index_name":       indexName,
			"table_size_bytes": tableSize,
			"index_size_bytes": indexSize,
			"ratio":            round2(ratio),
		}
		if includeSuggestions {
			entry["suggestion"] = fmt.Sprintf("Index is %.1fx larger than table — consider REINDEX CONCURRENTLY", ratio)
		}
		result = append(result, entry)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Action: schema — schema overview + config check + sequences
// ---------------------------------------------------------------------------

func handleSchema(ctx context.Context, deps DatabaseDeps, args map[string]any) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	resp := map[string]any{}

	// ---------- Schema overview ----------
	schema := "public"
	if v, ok := args["schema"].(string); ok && v != "" {
		schema = v
	}

	tableName, _ := args["table"].(string)

	if tableName != "" {
		schemaResult, err := schemaTableDetail(ctx, qe, schema, tableName)
		if err != nil {
			return nil, err
		}
		// For specific table detail, return directly.
		return schemaResult, nil
	}

	// All tables overview.
	tableQuery := fmt.Sprintf(`SELECT
		t.table_name,
		(SELECT count(*) FROM information_schema.columns c WHERE c.table_schema = t.table_schema AND c.table_name = t.table_name) AS column_count,
		pg_size_pretty(pg_total_relation_size(quote_ident(t.table_schema) || '.' || quote_ident(t.table_name))) AS size,
		obj_description((quote_ident(t.table_schema) || '.' || quote_ident(t.table_name))::regclass) AS comment,
		s.n_live_tup AS estimated_rows
	FROM information_schema.tables t
	LEFT JOIN pg_stat_user_tables s ON s.schemaname = t.table_schema AND s.relname = t.table_name
	WHERE t.table_schema = '%s' AND t.table_type = 'BASE TABLE'
	ORDER BY t.table_name`, schema)

	tableResult, err := qe.ExecuteReadQuery(ctx, tableQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("schema query failed: %v", err)), nil
	}

	if tableResult.RowCount == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No tables found in schema %q.", schema)), nil
	}

	tables := make([]map[string]any, 0, len(tableResult.Rows))
	for _, row := range tableResult.Rows {
		if len(row) < 5 {
			continue
		}
		t := map[string]any{
			"name":           row[0],
			"column_count":   row[1],
			"size":           row[2],
			"estimated_rows": row[4],
		}
		if row[3] != nil {
			t["comment"] = row[3]
		}
		tables = append(tables, t)
	}

	schemaOverview := map[string]any{
		"schema":      schema,
		"table_count": len(tables),
		"tables":      tables,
	}

	// Foreign key dependencies.
	depQuery := fmt.Sprintf(`SELECT
		tc.table_name AS from_table,
		ccu.table_name AS to_table,
		kcu.column_name AS from_column,
		ccu.column_name AS to_column
	FROM information_schema.table_constraints tc
	JOIN information_schema.key_column_usage kcu
		ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
	JOIN information_schema.constraint_column_usage ccu
		ON tc.constraint_name = ccu.constraint_name AND tc.table_schema = ccu.table_schema
	WHERE tc.table_schema = '%s' AND tc.constraint_type = 'FOREIGN KEY'
	ORDER BY tc.table_name`, schema)

	depResult, err := qe.ExecuteReadQuery(ctx, depQuery)
	if err == nil && depResult.RowCount > 0 {
		deps := make([]map[string]any, 0, len(depResult.Rows))
		for _, row := range depResult.Rows {
			if len(row) < 4 {
				continue
			}
			deps = append(deps, map[string]any{
				"from_table":  row[0],
				"to_table":    row[1],
				"from_column": row[2],
				"to_column":   row[3],
			})
		}
		schemaOverview["dependencies"] = deps
	}

	resp["schema_overview"] = schemaOverview

	// ---------- Config check ----------

	configQuery := `SELECT name, setting, unit, category, short_desc,
		CASE WHEN source = 'default' THEN 'default' ELSE source END AS source,
		boot_val, reset_val
	FROM pg_settings
	WHERE name IN (
		'shared_buffers', 'effective_cache_size', 'work_mem', 'maintenance_work_mem',
		'max_connections', 'max_worker_processes', 'max_parallel_workers',
		'max_parallel_workers_per_gather', 'wal_buffers', 'checkpoint_completion_target',
		'random_page_cost', 'effective_io_concurrency', 'max_wal_size', 'min_wal_size',
		'wal_level', 'max_replication_slots', 'hot_standby', 'archive_mode',
		'log_min_duration_statement', 'log_statement', 'log_lock_waits',
		'deadlock_timeout', 'lock_timeout', 'statement_timeout',
		'autovacuum', 'autovacuum_max_workers', 'autovacuum_naptime',
		'autovacuum_vacuum_threshold', 'autovacuum_vacuum_scale_factor',
		'autovacuum_analyze_threshold', 'autovacuum_analyze_scale_factor',
		'track_activity_query_size', 'default_statistics_target',
		'shared_preload_libraries', 'jit'
	)
	ORDER BY category, name`

	configResult, err := qe.ExecuteReadQuery(ctx, configQuery)
	if err == nil {
		infoQuery := `SELECT version(), pg_postmaster_start_time(),
			pg_size_pretty(pg_database_size(current_database())),
			current_database()`
		infoResult, _ := qe.ExecuteReadQuery(ctx, infoQuery)

		settings := make([]map[string]any, 0, len(configResult.Rows))
		var configWarnings []string

		for _, row := range configResult.Rows {
			if len(row) < 8 {
				continue
			}
			name := fmt.Sprintf("%v", row[0])
			setting := fmt.Sprintf("%v", row[1])
			unit := ""
			if row[2] != nil {
				unit = fmt.Sprintf("%v", row[2])
			}
			source := fmt.Sprintf("%v", row[5])

			s := map[string]any{
				"name":   name,
				"value":  setting,
				"source": source,
			}
			if unit != "" {
				s["unit"] = unit
			}
			if row[4] != nil {
				s["description"] = row[4]
			}
			settings = append(settings, s)

			configWarnings = append(configWarnings, checkConfigWarning(name, setting, unit)...)
		}

		configSection := map[string]any{
			"settings": settings,
			"total":    len(settings),
			"warnings": configWarnings,
		}

		if infoResult != nil && len(infoResult.Rows) > 0 && len(infoResult.Rows[0]) >= 4 {
			row := infoResult.Rows[0]
			configSection["server_info"] = map[string]any{
				"version":    row[0],
				"started_at": row[1],
				"db_size":    row[2],
				"database":   row[3],
			}
		}

		resp["config"] = configSection
	}

	// ---------- Sequence health ----------

	seqQuery := `SELECT
		schemaname || '.' || sequencename AS sequence_name,
		data_type,
		last_value,
		start_value,
		min_value,
		max_value,
		increment_by,
		cycle,
		CASE
			WHEN max_value > 0 AND last_value IS NOT NULL THEN
				ROUND(100.0 * (last_value - min_value) / NULLIF(max_value - min_value, 0), 2)
			ELSE 0
		END AS usage_pct
	FROM pg_sequences
	ORDER BY usage_pct DESC NULLS LAST`

	seqResult, err := qe.ExecuteReadQuery(ctx, seqQuery)
	if err == nil && seqResult.RowCount > 0 {
		sequences := make([]map[string]any, 0, len(seqResult.Rows))
		var seqWarnings []string

		for _, row := range seqResult.Rows {
			if len(row) < 9 {
				continue
			}
			name := fmt.Sprintf("%v", row[0])
			usagePct, _ := toFloat64(row[8])

			seq := map[string]any{
				"name":         name,
				"data_type":    row[1],
				"last_value":   row[2],
				"start_value":  row[3],
				"min_value":    row[4],
				"max_value":    row[5],
				"increment_by": row[6],
				"cycle":        row[7],
				"usage_pct":    usagePct,
			}
			sequences = append(sequences, seq)

			if usagePct > 75 {
				seqWarnings = append(seqWarnings, fmt.Sprintf("CRITICAL: %s is at %.1f%% capacity — will exhaust soon", name, usagePct))
			} else if usagePct > 50 {
				seqWarnings = append(seqWarnings, fmt.Sprintf("WARNING: %s is at %.1f%% capacity — monitor closely", name, usagePct))
			}

			if fmt.Sprintf("%v", row[1]) == "integer" && usagePct > 25 {
				seqWarnings = append(seqWarnings, fmt.Sprintf("%s uses integer type (max 2.1B) at %.1f%% — consider migrating to bigint", name, usagePct))
			}
		}

		resp["sequences"] = map[string]any{
			"sequences": sequences,
			"total":     len(sequences),
			"warnings":  seqWarnings,
		}
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// schemaTableDetail returns detailed information for a specific table.
func schemaTableDetail(ctx context.Context, qe connector.QueryExecutor, schema, tableName string) (*mcp.CallToolResult, error) {
	// Columns query.
	colQuery := fmt.Sprintf(`SELECT
		column_name,
		data_type,
		is_nullable,
		column_default,
		character_maximum_length
	FROM information_schema.columns
	WHERE table_schema = '%s' AND table_name = '%s'
	ORDER BY ordinal_position`, schema, tableName)

	colResult, err := qe.ExecuteReadQuery(ctx, colQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("columns query failed: %v", err)), nil
	}

	if colResult.RowCount == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("table %q not found in schema %q", tableName, schema)), nil
	}

	columns := make([]map[string]any, 0, len(colResult.Rows))
	for _, row := range colResult.Rows {
		if len(row) < 5 {
			continue
		}
		col := map[string]any{
			"name":     row[0],
			"type":     row[1],
			"nullable": row[2],
		}
		if row[3] != nil {
			col["default"] = row[3]
		}
		if row[4] != nil {
			col["max_length"] = row[4]
		}
		columns = append(columns, col)
	}

	// Indexes query.
	idxQuery := fmt.Sprintf(`SELECT
		indexname,
		indexdef
	FROM pg_indexes
	WHERE schemaname = '%s' AND tablename = '%s'
	ORDER BY indexname`, schema, tableName)

	indexes := make([]map[string]any, 0)
	idxResult, err := qe.ExecuteReadQuery(ctx, idxQuery)
	if err == nil {
		for _, row := range idxResult.Rows {
			if len(row) < 2 {
				continue
			}
			indexes = append(indexes, map[string]any{
				"name":       row[0],
				"definition": row[1],
			})
		}
	}

	// Foreign keys query.
	fkQuery := fmt.Sprintf(`SELECT
		tc.constraint_name,
		kcu.column_name,
		ccu.table_name AS foreign_table,
		ccu.column_name AS foreign_column
	FROM information_schema.table_constraints tc
	JOIN information_schema.key_column_usage kcu
		ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
	JOIN information_schema.constraint_column_usage ccu
		ON tc.constraint_name = ccu.constraint_name AND tc.table_schema = ccu.table_schema
	WHERE tc.table_schema = '%s' AND tc.table_name = '%s' AND tc.constraint_type = 'FOREIGN KEY'`, schema, tableName)

	foreignKeys := make([]map[string]any, 0)
	fkResult, err := qe.ExecuteReadQuery(ctx, fkQuery)
	if err == nil {
		for _, row := range fkResult.Rows {
			if len(row) < 4 {
				continue
			}
			foreignKeys = append(foreignKeys, map[string]any{
				"constraint":     row[0],
				"column":         row[1],
				"foreign_table":  row[2],
				"foreign_column": row[3],
			})
		}
	}

	resp := map[string]any{
		"schema":       schema,
		"table":        tableName,
		"columns":      columns,
		"column_count": len(columns),
		"indexes":      indexes,
		"foreign_keys": foreignKeys,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal table detail: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// checkConfigWarning returns warnings for known misconfiguration patterns.
func checkConfigWarning(name, setting, unit string) []string {
	var w []string
	val, _ := toFloat64(setting)

	switch name {
	case "shared_buffers":
		if unit == "8kB" && val < 16384 {
			w = append(w, "shared_buffers is below 128MB — recommended to set to 25% of total RAM")
		}
	case "work_mem":
		if unit == "kB" && val <= 4096 {
			w = append(w, "work_mem is at default 4MB — consider increasing for complex queries (8-64MB typical)")
		}
	case "maintenance_work_mem":
		if unit == "kB" && val <= 65536 {
			w = append(w, "maintenance_work_mem is low — increase to 256MB-1GB for faster VACUUM and index builds")
		}
	case "effective_cache_size":
		if unit == "8kB" && val < 65536 {
			w = append(w, "effective_cache_size is low — set to ~75% of total RAM for better query planning")
		}
	case "random_page_cost":
		if val >= 4 {
			w = append(w, "random_page_cost=4 is high for SSD storage — set to 1.1-1.5 for SSDs")
		}
	case "max_connections":
		if val > 200 {
			w = append(w, fmt.Sprintf("max_connections=%d is high — consider using a connection pooler (PgBouncer)", int(val)))
		}
	case "log_min_duration_statement":
		if setting == "-1" {
			w = append(w, "log_min_duration_statement is disabled — enable (e.g. 1000ms) to log slow queries")
		}
	case "autovacuum":
		if setting == "off" {
			w = append(w, "CRITICAL: autovacuum is disabled — this will cause table bloat and transaction ID wraparound")
		}
	case "log_lock_waits":
		if setting == "off" {
			w = append(w, "log_lock_waits is off — enable to detect lock contention in logs")
		}
	case "track_activity_query_size":
		if val < 4096 {
			w = append(w, "track_activity_query_size is small — increase to 4096+ to see full queries in pg_stat_activity")
		}
	case "default_statistics_target":
		if val <= 100 {
			w = append(w, "default_statistics_target=100 is default — increase to 200-500 for tables with skewed data distributions")
		}
	case "shared_preload_libraries":
		if !strings.Contains(setting, "pg_stat_statements") {
			w = append(w, "pg_stat_statements not in shared_preload_libraries — enable for query performance monitoring")
		}
	}
	return w
}

// ---------------------------------------------------------------------------
// Action: storage — disk usage + checkpoint stats + vacuum
// ---------------------------------------------------------------------------

func handleStorage(ctx context.Context, deps DatabaseDeps, args map[string]any) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	resp := map[string]any{}

	// ---------- Disk usage ----------

	dbQuery := `SELECT
		current_database() AS database,
		pg_size_pretty(pg_database_size(current_database())) AS total_size,
		pg_database_size(current_database()) AS total_bytes`
	dbResult, err := qe.ExecuteReadQuery(ctx, dbQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("database size query failed: %v", err)), nil
	}

	tableQuery := `SELECT
		schemaname || '.' || relname AS table_name,
		pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size,
		pg_total_relation_size(c.oid) AS total_bytes,
		pg_size_pretty(pg_table_size(c.oid)) AS table_size,
		pg_table_size(c.oid) AS table_bytes,
		pg_size_pretty(pg_indexes_size(c.oid)) AS index_size,
		pg_indexes_size(c.oid) AS index_bytes,
		pg_size_pretty(COALESCE(pg_total_relation_size(c.reltoastrelid), 0)) AS toast_size,
		COALESCE(pg_total_relation_size(c.reltoastrelid), 0) AS toast_bytes
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE c.relkind = 'r' AND n.nspname NOT IN ('pg_catalog', 'information_schema')
	ORDER BY pg_total_relation_size(c.oid) DESC
	LIMIT 30`

	tableResult, err := qe.ExecuteReadQuery(ctx, tableQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("table size query failed: %v", err)), nil
	}

	diskTables := make([]map[string]any, 0, len(tableResult.Rows))
	for _, row := range tableResult.Rows {
		if len(row) < 9 {
			continue
		}
		diskTables = append(diskTables, map[string]any{
			"table":       row[0],
			"total_size":  row[1],
			"total_bytes": row[2],
			"table_size":  row[3],
			"table_bytes": row[4],
			"index_size":  row[5],
			"index_bytes": row[6],
			"toast_size":  row[7],
			"toast_bytes": row[8],
		})
	}

	diskUsage := map[string]any{
		"tables":      diskTables,
		"table_count": len(diskTables),
	}

	if dbResult != nil && len(dbResult.Rows) > 0 && len(dbResult.Rows[0]) >= 3 {
		diskUsage["database"] = dbResult.Rows[0][0]
		diskUsage["database_size"] = dbResult.Rows[0][1]
		diskUsage["database_bytes"] = dbResult.Rows[0][2]
	}

	resp["disk_usage"] = diskUsage

	// ---------- Checkpoint stats ----------

	bgQuery := `SELECT
		checkpoints_timed,
		checkpoints_req,
		checkpoint_write_time,
		checkpoint_sync_time,
		buffers_checkpoint,
		buffers_clean,
		buffers_backend,
		maxwritten_clean,
		buffers_alloc,
		stats_reset
	FROM pg_stat_bgwriter`

	bgResult, err := qe.ExecuteReadQuery(ctx, bgQuery)
	if err == nil && len(bgResult.Rows) > 0 && len(bgResult.Rows[0]) >= 10 {
		row := bgResult.Rows[0]
		timedF, _ := toFloat64(row[0])
		reqF, _ := toFloat64(row[1])
		totalCkpt := timedF + reqF

		var reqPct float64
		if totalCkpt > 0 {
			reqPct = 100.0 * reqF / totalCkpt
		}

		var checkpointWarnings []string

		checkpoints := map[string]any{
			"checkpoints": map[string]any{
				"timed":          row[0],
				"requested":     row[1],
				"total":         totalCkpt,
				"requested_pct": fmt.Sprintf("%.1f%%", reqPct),
				"write_time_ms": row[2],
				"sync_time_ms":  row[3],
			},
			"buffers": map[string]any{
				"checkpoint":    row[4],
				"clean":         row[5],
				"backend":       row[6],
				"backend_fsync": row[7],
				"alloc":         row[8],
			},
			"stats_reset": row[9],
		}

		if reqPct > 50 {
			checkpointWarnings = append(checkpointWarnings, fmt.Sprintf("%.0f%% of checkpoints are requested (not timed) — increase max_wal_size or checkpoint_completion_target", reqPct))
		}

		backendF, _ := toFloat64(row[6])
		checkpointF, _ := toFloat64(row[4])
		if checkpointF > 0 && backendF/checkpointF > 0.1 {
			checkpointWarnings = append(checkpointWarnings, "High backend buffer writes — increase shared_buffers so the bgwriter handles more writes")
		}

		// Try pg_stat_wal for WAL generation rate (PG14+).
		walQuery := `SELECT wal_records, wal_fpi, wal_bytes,
			pg_size_pretty(wal_bytes::bigint) AS wal_size, stats_reset
		FROM pg_stat_wal`
		walResult, wErr := qe.ExecuteReadQuery(ctx, walQuery)
		if wErr == nil && len(walResult.Rows) > 0 && len(walResult.Rows[0]) >= 5 {
			wRow := walResult.Rows[0]
			checkpoints["wal"] = map[string]any{
				"records":          wRow[0],
				"full_page_images": wRow[1],
				"total_bytes":      wRow[2],
				"total_size":       wRow[3],
				"stats_reset":      wRow[4],
			}
		}

		// Current WAL position.
		walPosQuery := `SELECT pg_current_wal_lsn()::text, pg_walfile_name(pg_current_wal_lsn())`
		walPosResult, wErr := qe.ExecuteReadQuery(ctx, walPosQuery)
		if wErr == nil && len(walPosResult.Rows) > 0 && len(walPosResult.Rows[0]) >= 2 {
			checkpoints["wal_position"] = map[string]any{
				"lsn":  walPosResult.Rows[0][0],
				"file": walPosResult.Rows[0][1],
			}
		}

		checkpoints["warnings"] = checkpointWarnings
		resp["checkpoint_stats"] = checkpoints
	}

	// ---------- Vacuum report ----------

	vacQuery := `SELECT
		schemaname,
		relname AS table_name,
		n_live_tup,
		n_dead_tup,
		CASE WHEN n_live_tup + n_dead_tup > 0
			THEN ROUND(100.0 * n_dead_tup / (n_live_tup + n_dead_tup), 2)
			ELSE 0
		END AS dead_tuple_pct,
		last_vacuum,
		last_autovacuum,
		last_analyze,
		last_autoanalyze,
		vacuum_count,
		autovacuum_count,
		pg_size_pretty(pg_total_relation_size(quote_ident(schemaname) || '.' || quote_ident(relname))) AS total_size,
		n_tup_ins,
		n_tup_upd,
		n_tup_del
	FROM pg_stat_user_tables
	ORDER BY n_dead_tup DESC
	LIMIT 50`

	vacResult, err := qe.ExecuteReadQuery(ctx, vacQuery)
	if err == nil && vacResult.RowCount > 0 {
		vacTables := make([]map[string]any, 0, len(vacResult.Rows))
		var vacRecommendations []string

		for _, row := range vacResult.Rows {
			if len(row) < 15 {
				continue
			}
			vt := map[string]any{
				"schema":           row[0],
				"table":            row[1],
				"live_tuples":      row[2],
				"dead_tuples":      row[3],
				"dead_tuple_pct":   row[4],
				"last_vacuum":      row[5],
				"last_autovacuum":  row[6],
				"last_analyze":     row[7],
				"last_autoanalyze": row[8],
				"vacuum_count":     row[9],
				"autovacuum_count": row[10],
				"total_size":       row[11],
				"inserts":          row[12],
				"updates":          row[13],
				"deletes":          row[14],
			}
			vacTables = append(vacTables, vt)

			vtName := fmt.Sprintf("%v.%v", row[0], row[1])
			if pct, ok := toFloat64(row[4]); ok && pct > 10 {
				vacRecommendations = append(vacRecommendations, fmt.Sprintf("VACUUM ANALYZE %s — %.1f%% dead tuples", vtName, pct))
			}
			if row[5] == nil && row[6] == nil {
				vacRecommendations = append(vacRecommendations, fmt.Sprintf("VACUUM %s — never vacuumed", vtName))
			}

			updates, _ := toFloat64(row[13])
			deletes, _ := toFloat64(row[14])
			if updates+deletes > 100000 && row[5] == nil && row[6] == nil {
				vacRecommendations = append(vacRecommendations, fmt.Sprintf("%s has %.0f updates + %.0f deletes but was never vacuumed", vtName, updates, deletes))
			}
		}

		// Fetch autovacuum settings.
		avQuery := `SELECT name, setting FROM pg_settings
			WHERE name IN ('autovacuum', 'autovacuum_vacuum_threshold', 'autovacuum_vacuum_scale_factor',
				'autovacuum_analyze_threshold', 'autovacuum_analyze_scale_factor', 'autovacuum_naptime',
				'autovacuum_max_workers')`
		avSettings := make(map[string]string)
		avResult, avErr := qe.ExecuteReadQuery(ctx, avQuery)
		if avErr == nil {
			for _, row := range avResult.Rows {
				if len(row) >= 2 {
					avSettings[fmt.Sprintf("%v", row[0])] = fmt.Sprintf("%v", row[1])
				}
			}
		}

		resp["vacuum_report"] = map[string]any{
			"tables":              vacTables,
			"total_tables":        len(vacTables),
			"recommendations":     vacRecommendations,
			"autovacuum_settings": avSettings,
		}
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: kill_query — terminate a query (admin action)
// ---------------------------------------------------------------------------

func handleKillQuery(ctx context.Context, deps DatabaseDeps, args map[string]any) (*mcp.CallToolResult, error) {
	pidFloat, ok := args["pid"].(float64)
	if !ok || pidFloat <= 0 {
		return mcp.NewToolResultError("pid is required (positive integer). Use database action=activity to find PIDs of long-running queries."), nil
	}
	pid := int(pidFloat)

	force := false
	if v, ok := args["force"].(bool); ok {
		force = v
	}

	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	// First, check what the process is doing.
	infoQuery := fmt.Sprintf(`SELECT pid, state, COALESCE(application_name, '') AS app_name,
		LEFT(query, 300) AS query_preview,
		EXTRACT(EPOCH FROM (now() - query_start))::int AS duration_seconds
	FROM pg_stat_activity WHERE pid = %d`, pid)

	infoResult, err := qe.ExecuteReadQuery(ctx, infoQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to look up PID %d: %v", pid, err)), nil
	}

	if infoResult.RowCount == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("no active backend with PID %d found. The query may have already completed. Use database action=activity to check current queries.", pid)), nil
	}

	var state, appName, queryPreview string
	var durationSec int64
	if len(infoResult.Rows) > 0 && len(infoResult.Rows[0]) >= 5 {
		row := infoResult.Rows[0]
		state = toString(row[1])
		appName = toString(row[2])
		queryPreview = toString(row[3])
		durationSec = toInt64(row[4])
	}

	// Cancel or terminate.
	var actionQuery string
	var action string
	if force {
		action = "terminated"
		actionQuery = fmt.Sprintf("SELECT pg_terminate_backend(%d)", pid)
	} else {
		action = "cancelled"
		actionQuery = fmt.Sprintf("SELECT pg_cancel_backend(%d)", pid)
	}

	result, err := qe.ExecuteReadQuery(ctx, actionQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to %s PID %d: %v", action[:len(action)-2], pid, err)), nil
	}

	success := false
	if len(result.Rows) > 0 && len(result.Rows[0]) > 0 {
		if v, ok := result.Rows[0][0].(bool); ok {
			success = v
		}
	}

	resp := map[string]any{
		"pid":              pid,
		"action":           action,
		"success":          success,
		"state_before":     state,
		"application_name": appName,
		"query_preview":    queryPreview,
		"duration_seconds": durationSec,
	}

	if !success {
		resp["note"] = fmt.Sprintf("pg_%s_backend returned false — the process may have already ended or you may lack permission. Use database action=activity to verify.", action[:len(action)-1])
	}

	if !force && success {
		resp["hint"] = "Query was cancelled gracefully. If the process doesn't stop, use force=true to terminate it."
	}

	// Track killed PID on session.
	if success && deps.SessionTracking != nil {
		deps.SessionTracking.TrackKilledQuery(fmt.Sprintf("%d", pid))
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Action: long_transactions — find long-running sessions
// ---------------------------------------------------------------------------

func handleLongTransactions(ctx context.Context, deps DatabaseDeps, args map[string]any) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	minDurationSec := 30.0
	if v, ok := args["min_duration_seconds"].(float64); ok && v > 0 {
		minDurationSec = v
	}

	query := fmt.Sprintf(`SELECT
		pid,
		usename,
		application_name,
		client_addr::text,
		state,
		EXTRACT(EPOCH FROM (now() - xact_start))::int AS xact_duration_sec,
		EXTRACT(EPOCH FROM (now() - state_change))::int AS state_duration_sec,
		EXTRACT(EPOCH FROM (now() - query_start))::int AS query_duration_sec,
		wait_event_type,
		wait_event,
		LEFT(query, 200) AS query_preview,
		xact_start,
		state_change
	FROM pg_stat_activity
	WHERE xact_start IS NOT NULL
		AND pid != pg_backend_pid()
		AND EXTRACT(EPOCH FROM (now() - xact_start)) > %v
	ORDER BY xact_start ASC`, minDurationSec)

	result, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("long transactions query failed: %v", err)), nil
	}

	if result.RowCount == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No transactions running longer than %.0f seconds.", minDurationSec)), nil
	}

	transactions := make([]map[string]any, 0, len(result.Rows))
	var idleInTx int
	var warnings []string

	for _, row := range result.Rows {
		if len(row) < 13 {
			continue
		}
		state := fmt.Sprintf("%v", row[4])
		xactDur, _ := toFloat64(row[5])

		tx := map[string]any{
			"pid":                row[0],
			"user":               row[1],
			"application":        row[2],
			"client_addr":        row[3],
			"state":              state,
			"xact_duration_sec":  row[5],
			"state_duration_sec": row[6],
			"query_duration_sec": row[7],
			"wait_event_type":    row[8],
			"wait_event":         row[9],
			"query_preview":      row[10],
			"xact_start":         row[11],
			"state_change":       row[12],
		}
		transactions = append(transactions, tx)

		if state == "idle in transaction" || state == "idle in transaction (aborted)" {
			idleInTx++
			if xactDur > 300 {
				warnings = append(warnings, fmt.Sprintf("PID %v is idle in transaction for %.0f seconds — likely a leaked connection. Consider killing with database action=kill_query.", row[0], xactDur))
			}
		}
		if xactDur > 3600 {
			warnings = append(warnings, fmt.Sprintf("PID %v has a transaction open for %.0f seconds (>1hr) — prevents vacuum from cleaning dead tuples", row[0], xactDur))
		}
	}

	// Check what locks these transactions hold.
	lockQuery := `SELECT
		a.pid,
		l.locktype,
		l.mode,
		COALESCE(l.relation::regclass::text, '') AS relation,
		l.granted
	FROM pg_locks l
	JOIN pg_stat_activity a ON a.pid = l.pid
	WHERE a.xact_start IS NOT NULL
		AND a.pid != pg_backend_pid()
		AND l.granted = true
		AND l.locktype IN ('relation', 'tuple', 'advisory')
	ORDER BY a.pid`

	locks := make([]map[string]any, 0)
	lockResult, err := qe.ExecuteReadQuery(ctx, lockQuery)
	if err == nil {
		for _, row := range lockResult.Rows {
			if len(row) < 5 {
				continue
			}
			locks = append(locks, map[string]any{
				"pid":      row[0],
				"locktype": row[1],
				"mode":     row[2],
				"relation": row[3],
				"granted":  row[4],
			})
		}
	}

	resp := map[string]any{
		"transactions":        transactions,
		"total":               len(transactions),
		"idle_in_transaction": idleInTx,
		"locks_held":          locks,
		"warnings":            warnings,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Runbook implementations
// ---------------------------------------------------------------------------

func runbookSlowDB(ctx context.Context, registry *connector.Registry) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(registry)
	if errResult != nil {
		return errResult, nil
	}

	sections := make(map[string]any)
	var diagnosis []string

	// 1. Active connections summary.
	activityQ := `SELECT state, count(*) FROM pg_stat_activity WHERE pid != pg_backend_pid() GROUP BY state`
	if r, err := qe.ExecuteReadQuery(ctx, activityQ); err == nil {
		states := make(map[string]any)
		for _, row := range r.Rows {
			if len(row) >= 2 {
				states[fmt.Sprintf("%v", row[0])] = row[1]
			}
		}
		sections["connections"] = states
	}

	// 2. Long-running queries (>5s).
	longQ := `SELECT pid, usename, state,
		EXTRACT(EPOCH FROM (now() - query_start))::int AS duration_sec,
		LEFT(query, 150) AS query
	FROM pg_stat_activity
	WHERE state = 'active' AND pid != pg_backend_pid()
		AND query_start < now() - interval '5 seconds'
	ORDER BY query_start LIMIT 10`
	if r, err := qe.ExecuteReadQuery(ctx, longQ); err == nil && r.RowCount > 0 {
		queries := make([]map[string]any, 0, len(r.Rows))
		for _, row := range r.Rows {
			if len(row) >= 5 {
				queries = append(queries, map[string]any{
					"pid": row[0], "user": row[1], "state": row[2],
					"duration_sec": row[3], "query": row[4],
				})
			}
		}
		sections["long_running_queries"] = queries
		diagnosis = append(diagnosis, fmt.Sprintf("%d queries running >5 seconds", len(queries)))
	}

	// 3. Lock contention.
	lockQ := `SELECT blocked.pid AS blocked_pid,
		blocking.pid AS blocking_pid,
		LEFT(blocked.query, 100) AS blocked_query,
		LEFT(blocking.query, 100) AS blocking_query
	FROM pg_stat_activity blocked
	JOIN pg_locks bl ON bl.pid = blocked.pid AND NOT bl.granted
	JOIN pg_locks gl ON gl.pid != blocked.pid AND gl.locktype = bl.locktype
		AND gl.database IS NOT DISTINCT FROM bl.database
		AND gl.relation IS NOT DISTINCT FROM bl.relation
		AND gl.granted
	JOIN pg_stat_activity blocking ON blocking.pid = gl.pid
	LIMIT 10`
	if r, err := qe.ExecuteReadQuery(ctx, lockQ); err == nil && r.RowCount > 0 {
		chains := make([]map[string]any, 0)
		for _, row := range r.Rows {
			if len(row) >= 4 {
				chains = append(chains, map[string]any{
					"blocked_pid": row[0], "blocking_pid": row[1],
					"blocked_query": row[2], "blocking_query": row[3],
				})
			}
		}
		sections["lock_contention"] = chains
		diagnosis = append(diagnosis, fmt.Sprintf("%d blocking chains found", len(chains)))
	}

	// 4. Top slow queries from pg_stat_statements.
	statsQ := `SELECT LEFT(query, 150), calls, mean_exec_time::numeric(10,2),
		total_exec_time::numeric(10,2)
	FROM pg_stat_statements
	ORDER BY mean_exec_time DESC LIMIT 5`
	if r, err := qe.ExecuteReadQuery(ctx, statsQ); err == nil && r.RowCount > 0 {
		top := make([]map[string]any, 0)
		for _, row := range r.Rows {
			if len(row) >= 4 {
				top = append(top, map[string]any{
					"query": row[0], "calls": row[1],
					"mean_time_ms": row[2], "total_time_ms": row[3],
				})
			}
		}
		sections["top_slow_queries"] = top
	}

	// 5. Cache hit ratio.
	cacheQ := `SELECT
		ROUND(100.0 * sum(blks_hit) / NULLIF(sum(blks_hit) + sum(blks_read), 0), 2) AS cache_hit_pct,
		sum(blks_hit) AS hits, sum(blks_read) AS reads
	FROM pg_stat_database WHERE datname = current_database()`
	if r, err := qe.ExecuteReadQuery(ctx, cacheQ); err == nil && len(r.Rows) > 0 && len(r.Rows[0]) >= 3 {
		hitPct, _ := toFloat64(r.Rows[0][0])
		sections["cache_hit_ratio"] = map[string]any{
			"hit_pct": r.Rows[0][0], "hits": r.Rows[0][1], "reads": r.Rows[0][2],
		}
		if hitPct < 95 {
			diagnosis = append(diagnosis, fmt.Sprintf("Cache hit ratio is %.1f%% (below 95%%) — consider increasing shared_buffers", hitPct))
		}
	}

	if len(diagnosis) == 0 {
		diagnosis = append(diagnosis, "No obvious performance issues detected")
	}

	resp := map[string]any{
		"playbook":  "slow_database",
		"sections":  sections,
		"diagnosis": diagnosis,
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func runbookConnExhaustion(ctx context.Context, registry *connector.Registry) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(registry)
	if errResult != nil {
		return errResult, nil
	}

	sections := make(map[string]any)
	var diagnosis []string

	// Connection utilization.
	connQ := `SELECT
		(SELECT count(*) FROM pg_stat_activity) AS current,
		(SELECT setting::int FROM pg_settings WHERE name = 'max_connections') AS max,
		ROUND(100.0 * (SELECT count(*) FROM pg_stat_activity) /
			(SELECT setting::int FROM pg_settings WHERE name = 'max_connections'), 1) AS pct`
	if r, err := qe.ExecuteReadQuery(ctx, connQ); err == nil && len(r.Rows) > 0 && len(r.Rows[0]) >= 3 {
		pct, _ := toFloat64(r.Rows[0][2])
		sections["utilization"] = map[string]any{
			"current": r.Rows[0][0], "max": r.Rows[0][1], "pct": r.Rows[0][2],
		}
		if pct > 80 {
			diagnosis = append(diagnosis, fmt.Sprintf("Connection utilization at %.0f%% — critical", pct))
		}
	}

	// By application.
	appQ := `SELECT COALESCE(application_name, 'unknown'), state, count(*)
	FROM pg_stat_activity WHERE pid != pg_backend_pid()
	GROUP BY application_name, state ORDER BY count(*) DESC LIMIT 20`
	if r, err := qe.ExecuteReadQuery(ctx, appQ); err == nil && r.RowCount > 0 {
		apps := make([]map[string]any, 0)
		for _, row := range r.Rows {
			if len(row) >= 3 {
				apps = append(apps, map[string]any{
					"application": row[0], "state": row[1], "count": row[2],
				})
			}
		}
		sections["by_application"] = apps
	}

	// Idle in transaction.
	idleQ := `SELECT pid, usename, application_name,
		EXTRACT(EPOCH FROM (now() - xact_start))::int AS duration_sec
	FROM pg_stat_activity
	WHERE state = 'idle in transaction' AND pid != pg_backend_pid()
	ORDER BY xact_start LIMIT 10`
	if r, err := qe.ExecuteReadQuery(ctx, idleQ); err == nil && r.RowCount > 0 {
		idleList := make([]map[string]any, 0)
		for _, row := range r.Rows {
			if len(row) >= 4 {
				idleList = append(idleList, map[string]any{
					"pid": row[0], "user": row[1], "app": row[2], "duration_sec": row[3],
				})
			}
		}
		sections["idle_in_transaction"] = idleList
		diagnosis = append(diagnosis, fmt.Sprintf("%d idle-in-transaction sessions — likely leaked connections", len(idleList)))
	}

	if len(diagnosis) == 0 {
		diagnosis = append(diagnosis, "Connection pool looks healthy")
	}

	resp := map[string]any{"playbook": "connection_exhaustion", "sections": sections, "diagnosis": diagnosis}
	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func runbookDiskPressure(ctx context.Context, registry *connector.Registry) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(registry)
	if errResult != nil {
		return errResult, nil
	}

	sections := make(map[string]any)
	var diagnosis []string

	// Database size.
	dbQ := `SELECT current_database(), pg_size_pretty(pg_database_size(current_database())),
		pg_database_size(current_database())`
	if r, err := qe.ExecuteReadQuery(ctx, dbQ); err == nil && len(r.Rows) > 0 {
		sections["database"] = map[string]any{
			"name": r.Rows[0][0], "size": r.Rows[0][1], "bytes": r.Rows[0][2],
		}
	}

	// Top tables by size.
	topQ := `SELECT schemaname || '.' || relname,
		pg_size_pretty(pg_total_relation_size(quote_ident(schemaname)||'.'||quote_ident(relname))),
		n_dead_tup,
		ROUND(100.0 * n_dead_tup / GREATEST(n_live_tup + n_dead_tup, 1), 1) AS dead_pct
	FROM pg_stat_user_tables
	ORDER BY pg_total_relation_size(quote_ident(schemaname)||'.'||quote_ident(relname)) DESC LIMIT 10`
	if r, err := qe.ExecuteReadQuery(ctx, topQ); err == nil && r.RowCount > 0 {
		topTables := make([]map[string]any, 0)
		for _, row := range r.Rows {
			if len(row) >= 4 {
				topTables = append(topTables, map[string]any{
					"table": row[0], "size": row[1], "dead_tuples": row[2], "dead_pct": row[3],
				})
				deadPct, _ := toFloat64(row[3])
				if deadPct > 20 {
					diagnosis = append(diagnosis, fmt.Sprintf("%v has %.0f%% dead tuples — needs VACUUM", row[0], deadPct))
				}
			}
		}
		sections["top_tables"] = topTables
	}

	// WAL size.
	walQ := `SELECT pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), '0/0'))`
	if r, err := qe.ExecuteReadQuery(ctx, walQ); err == nil && len(r.Rows) > 0 {
		sections["wal_total"] = r.Rows[0][0]
	}

	if len(diagnosis) == 0 {
		diagnosis = append(diagnosis, "No obvious disk pressure issues")
	}

	resp := map[string]any{"playbook": "disk_pressure", "sections": sections, "diagnosis": diagnosis}
	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func runbookReplicationLag(ctx context.Context, registry *connector.Registry) (*mcp.CallToolResult, error) {
	qe, errResult := getQueryExecutor(registry)
	if errResult != nil {
		return errResult, nil
	}

	sections := make(map[string]any)
	var diagnosis []string

	// Server role.
	roleQ := `SELECT pg_is_in_recovery()`
	if r, err := qe.ExecuteReadQuery(ctx, roleQ); err == nil && len(r.Rows) > 0 {
		isReplica := fmt.Sprintf("%v", r.Rows[0][0]) == "true"
		if isReplica {
			sections["role"] = "replica"
		} else {
			sections["role"] = "primary"
		}
	}

	// Replication slots.
	slotQ := `SELECT slot_name, slot_type, active,
		pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS lag
	FROM pg_replication_slots`
	if r, err := qe.ExecuteReadQuery(ctx, slotQ); err == nil && r.RowCount > 0 {
		slots := make([]map[string]any, 0)
		for _, row := range r.Rows {
			if len(row) >= 4 {
				activeStr := fmt.Sprintf("%v", row[2])
				slots = append(slots, map[string]any{
					"name": row[0], "type": row[1], "active": row[2], "lag": row[3],
				})
				if activeStr == "false" {
					diagnosis = append(diagnosis, fmt.Sprintf("Replication slot %v is inactive — may cause WAL bloat", row[0]))
				}
			}
		}
		sections["slots"] = slots
	}

	// Connected replicas.
	replQ := `SELECT client_addr::text, state, sent_lsn, write_lsn, flush_lsn, replay_lsn,
		pg_size_pretty(pg_wal_lsn_diff(sent_lsn, replay_lsn)) AS replay_lag
	FROM pg_stat_replication`
	if r, err := qe.ExecuteReadQuery(ctx, replQ); err == nil && r.RowCount > 0 {
		replicas := make([]map[string]any, 0)
		for _, row := range r.Rows {
			if len(row) >= 7 {
				replicas = append(replicas, map[string]any{
					"client": row[0], "state": row[1], "replay_lag": row[6],
				})
			}
		}
		sections["replicas"] = replicas
	} else {
		sections["replicas"] = "none connected"
	}

	if len(diagnosis) == 0 {
		diagnosis = append(diagnosis, "Replication looks healthy")
	}

	resp := map[string]any{"playbook": "replication_lag", "sections": sections, "diagnosis": diagnosis}
	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func runbookErrorSpike(ctx context.Context, logStore store.LogStore) (*mcp.CallToolResult, error) {
	sections := make(map[string]any)
	var diagnosis []string
	now := time.Now().UTC()

	// Error log distribution by service.
	if logStore != nil {
		since := now.Add(-1 * time.Hour)
		counts, err := logStore.CountByLevel(ctx, store.LogCountParams{
			Since: since, Until: now,
		})
		if err == nil {
			sections["log_levels_last_hour"] = counts
			if errorCount, ok := counts["error"]; ok && errorCount > 50 {
				diagnosis = append(diagnosis, fmt.Sprintf("%d errors in last hour", errorCount))
			}
			if fatalCount, ok := counts["fatal"]; ok && fatalCount > 0 {
				diagnosis = append(diagnosis, fmt.Sprintf("%d FATAL logs in last hour — service crashes", fatalCount))
			}
		}

		// Top error services.
		svcCounts, err := logStore.CountByService(ctx, store.LogCountParams{
			Since: since, Until: now, Level: "error",
		})
		if err == nil && len(svcCounts) > 0 {
			services := make([]map[string]any, 0, len(svcCounts))
			for _, sc := range svcCounts {
				services = append(services, map[string]any{
					"service": sc.Service, "count": sc.Total,
				})
			}
			sections["top_error_services"] = services
		}

		// Recent error messages.
		errors, err := logStore.Search(ctx, store.LogSearchParams{
			Level: "error", Start: &since, End: &now, Limit: 10,
		})
		if err == nil && len(errors) > 0 {
			msgs := make([]map[string]any, 0, len(errors))
			for _, e := range errors {
				msg := e.Message
				if len(msg) > 200 {
					msg = msg[:200] + "..."
				}
				msgs = append(msgs, map[string]any{
					"time": e.Timestamp.Format(time.RFC3339), "service": e.Service, "message": msg,
				})
			}
			sections["recent_errors"] = msgs
		}
	}

	if len(diagnosis) == 0 {
		diagnosis = append(diagnosis, "No error spike detected in last hour")
	}

	resp := map[string]any{"playbook": "error_spike", "sections": sections, "diagnosis": diagnosis}
	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

// ---------------------------------------------------------------------------
// Utility functions (local to tools package)
// ---------------------------------------------------------------------------

// toInt64 converts an any value to int64, returning 0 if conversion fails.
func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case nil:
		return 0
	default:
		return 0
	}
}

// toFloat64 attempts to convert an interface value to float64.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case string:
		var f float64
		_, err := fmt.Sscanf(n, "%f", &f)
		return f, err == nil
	default:
		return 0, false
	}
}

// toString converts an any value to string.
func toString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// sanitizeIdentifier removes characters that aren't valid in a SQL identifier.
func sanitizeIdentifier(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// humanSize formats bytes into human-readable size.
func humanSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// parsePostgresArray parses a simple Postgres array literal like {a,b,c}.
func parsePostgresArray(s string) []string {
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
