package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"


	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/store"
)

// RunbookHandler returns the handler for the runbook tool.
func RunbookHandler(deps RunbookDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)

		playbook, _ := args["playbook"].(string)
		if playbook == "" {
			return NewToolResultError("playbook is required. Available: slow_database, connection_exhaustion, disk_pressure, replication_lag, error_spike"), nil
		}

		var result *CallToolResult
		var err error

		switch playbook {
		case "slow_database":
			result, err = RunbookSlowDB(ctx, deps.Registry)
		case "connection_exhaustion":
			result, err = RunbookConnExhaustion(ctx, deps.Registry)
		case "disk_pressure":
			result, err = RunbookDiskPressure(ctx, deps.Registry)
		case "replication_lag":
			result, err = RunbookReplicationLag(ctx, deps.Registry)
		case "error_spike":
			result, err = RunbookErrorSpike(ctx, deps.LogStore)
		default:
			return NewToolResultError(fmt.Sprintf("unknown playbook %q. Available: slow_database, connection_exhaustion, disk_pressure, replication_lag, error_spike", playbook)), nil
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
// Runbook implementations
// ---------------------------------------------------------------------------

func RunbookSlowDB(ctx context.Context, registry *connector.Registry) (*CallToolResult, error) {
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
	return NewToolResultText(string(data)), nil
}

func RunbookConnExhaustion(ctx context.Context, registry *connector.Registry) (*CallToolResult, error) {
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
	return NewToolResultText(string(data)), nil
}

func RunbookDiskPressure(ctx context.Context, registry *connector.Registry) (*CallToolResult, error) {
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
	return NewToolResultText(string(data)), nil
}

func RunbookReplicationLag(ctx context.Context, registry *connector.Registry) (*CallToolResult, error) {
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
	return NewToolResultText(string(data)), nil
}

func RunbookErrorSpike(ctx context.Context, logStore store.LogStore) (*CallToolResult, error) {
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
	return NewToolResultText(string(data)), nil
}
