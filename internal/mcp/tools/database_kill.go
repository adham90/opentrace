package tools

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/connector"
)

// ---------------------------------------------------------------------------
// Action: kill_query — terminate a query (admin action)
// ---------------------------------------------------------------------------

func HandleKillQuery(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	// kill_query runs pg_cancel_backend / pg_terminate_backend, which are
	// destructive. Restrict it to admins — members get a clear error.
	if !deps.IsAdmin {
		return NewToolResultError("admin privileges are required to cancel or terminate database backends (kill_query)"), nil
	}

	pidFloat, ok := args["pid"].(float64)
	if !ok || pidFloat <= 0 {
		return NewToolResultError("pid is required (positive integer). Use database action=activity to find PIDs of long-running queries."), nil
	}
	pid := int(pidFloat)

	force := ArgBool(args, "force")

	qe, errResult := getQueryExecutor(ctx, deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	// The cancel/terminate call must run through the privileged control path:
	// the read-only guardrail now rejects pg_terminate_backend / pg_cancel_backend
	// as side-effecting functions, so ExecuteReadQuery would refuse them.
	control, ok := qe.(connector.ControlExecutor)
	if !ok {
		return NewToolResultError("the active database connector does not support cancelling or terminating backends"), nil
	}

	// First, check what the process is doing.
	infoQuery := fmt.Sprintf(`SELECT pid, state, COALESCE(application_name, '') AS app_name,
		LEFT(query, 300) AS query_preview,
		EXTRACT(EPOCH FROM (now() - query_start))::int AS duration_seconds
	FROM pg_stat_activity WHERE pid = %d`, pid)

	infoResult, err := qe.ExecuteReadQuery(ctx, infoQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to look up PID %d: %v", pid, err)), nil
	}

	if infoResult.RowCount == 0 {
		return NewToolResultError(fmt.Sprintf("no active backend with PID %d found. The query may have already completed. Use database action=activity to check current queries.", pid)), nil
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

	result, err := control.ExecuteControlQuery(ctx, actionQuery)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to %s PID %d: %v", action[:len(action)-2], pid, err)), nil
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

	return JSONResult(resp)
}

// ---------------------------------------------------------------------------
// Action: long_transactions — find long-running sessions
// ---------------------------------------------------------------------------

func HandleLongTransactions(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	qe, errResult := getQueryExecutor(ctx, deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	minDurationSec := ArgFloat(args, "min_duration_seconds", 30.0)

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
		return NewToolResultError(fmt.Sprintf("long transactions query failed: %v", err)), nil
	}

	if result.RowCount == 0 {
		return NewToolResultText(fmt.Sprintf("No transactions running longer than %.0f seconds.", minDurationSec)), nil
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

	return JSONResult(resp)
}
