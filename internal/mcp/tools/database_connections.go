package tools

import (
	"context"
	"fmt"
	"strconv"
)

// ---------------------------------------------------------------------------
// Action: activity — current connections/activity
// ---------------------------------------------------------------------------

func HandleDatabaseActivity(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
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
		return NewToolResultError(fmt.Sprintf("failed to query connection summary: %v", err)), nil
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
		return NewToolResultError(fmt.Sprintf("failed to query long-running queries: %v", err)), nil
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
		return NewToolResultError(fmt.Sprintf("failed to query idle-in-transaction sessions: %v", err)), nil
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

	return JSONResult(resp)
}

// ---------------------------------------------------------------------------
// Action: connections — pool stats + replication
// ---------------------------------------------------------------------------

func HandleConnections(ctx context.Context, deps DatabaseDeps, args map[string]any) (*CallToolResult, error) {
	qe, errResult := getQueryExecutor(deps.Registry)
	if errResult != nil {
		return errResult, nil
	}

	resp := map[string]any{}

	// ---------- Pool stats ----------

	// Get max connections setting.
	maxConnResult, err := qe.ExecuteReadQuery(ctx, `SELECT current_setting('max_connections')`)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query max_connections: %v", err)), nil
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
		return NewToolResultError(fmt.Sprintf("failed to query connection stats: %v", err)), nil
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
		return NewToolResultError(fmt.Sprintf("failed to query application stats: %v", err)), nil
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
		return JSONResult(resp)
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

	return JSONResult(resp)
}
