package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/adham90/opentrace/internal/guardrail"
)

// ---------------------------------------------------------------------------
// Action: queries — query stats from pg_stat_statements
// ---------------------------------------------------------------------------

func HandleQueries(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	orderBy := ArgStringDefault(args, "order_by", "total_exec_time")
	allowed := map[string]bool{
		"calls": true, "total_exec_time": true, "mean_exec_time": true,
		"rows": true, "shared_blks_hit": true, "shared_blks_read": true,
	}
	if !allowed[orderBy] {
		orderBy = "total_exec_time"
	}

	limit := ArgInt(args, "limit", 20, 100)
	filter := ArgString(args, "filter")

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
			return NewToolResultText(
				"pg_stat_statements extension is not enabled.\n\n" +
					"To enable it:\n" +
					"1. Add to postgresql.conf: shared_preload_libraries = 'pg_stat_statements'\n" +
					"2. Restart PostgreSQL\n" +
					"3. Run: CREATE EXTENSION IF NOT EXISTS pg_stat_statements;\n\n" +
					"This extension tracks query execution statistics and is very useful for identifying slow queries."), nil
		}
		return NewToolResultError(fmt.Sprintf("failed to query pg_stat_statements: %v", err)), nil
	}

	if result.RowCount == 0 {
		return NewToolResultText("No query statistics found. The database may have been recently restarted, or pg_stat_statements may have no recorded queries yet."), nil
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

	return JSONResult(resp)
}

// ---------------------------------------------------------------------------
// Action: explain — EXPLAIN ANALYZE a query
// ---------------------------------------------------------------------------

// queryFingerprintRe strips string/numeric literals to create a query fingerprint.
func HandleExplain(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	query := ArgString(args, "query")
	if query == "" {
		return NewToolResultError("query is required for the explain action"), nil
	}

	// Validate that it's a SELECT statement.
	if err := guardrail.ValidateReadOnlyGeneric(query); err != nil {
		return NewToolResultError(fmt.Sprintf("only SELECT queries can be explained: %v", err)), nil
	}

	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	analyze := ArgBool(args, "analyze")

	buffers := true
	if v, ok := args["buffers"].(bool); ok {
		buffers = v
	}

	outputFormat := ArgStringDefault(args, "format", "text")
	if outputFormat != "json" && outputFormat != "text" {
		outputFormat = "text"
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
		return NewToolResultError(fmt.Sprintf("EXPLAIN failed: %v", err)), nil
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

	return JSONResult(resp)
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
