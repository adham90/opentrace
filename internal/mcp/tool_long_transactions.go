package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/connector"
)

// longTransactionsHandler returns a handler that detects long-running and idle-in-transaction sessions.
func longTransactionsHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		minDurationSec := 30.0
		if v, ok := args["min_duration_seconds"].(float64); ok && v > 0 {
			minDurationSec = v
		}

		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active."), nil
		}
		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("active database connector does not support direct queries"), nil
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
					warnings = append(warnings, fmt.Sprintf("PID %v is idle in transaction for %.0f seconds — likely a leaked connection. Consider killing with kill_query.", row[0], xactDur))
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
			"transactions":      transactions,
			"total":             len(transactions),
			"idle_in_transaction": idleInTx,
			"locks_held":        locks,
			"warnings":          warnings,
		}

		data, err := json.Marshal(resp)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
