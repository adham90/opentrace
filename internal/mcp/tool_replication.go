package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/connector"
)

// replicationStatusHandler returns a handler that checks PostgreSQL replication
// status: replica lag, slot status, and WAL archival.
func replicationStatusHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active. Connect a PostgreSQL data source first."), nil
		}

		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("The active database connector does not support direct queries."), nil
		}

		resp := map[string]any{}
		var warnings []string

		// 1. Check if this is a primary or replica.
		roleResult, err := qe.ExecuteReadQuery(ctx, "SELECT pg_is_in_recovery()")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to check server role: %v. Verify the database connector is working with list_connectors.", err)), nil
		}

		isReplica := false
		if len(roleResult.Rows) > 0 && len(roleResult.Rows[0]) > 0 {
			if v, ok := roleResult.Rows[0][0].(bool); ok {
				isReplica = v
			}
		}

		if isReplica {
			resp["role"] = "replica"
		} else {
			resp["role"] = "primary"
		}

		// 2. Replication slots (primary only, but query is safe on replicas — returns empty).
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
				active := false
				if v, ok := row[2].(bool); ok {
					active = v
				}
				slot := map[string]any{
					"slot_name":           toString(row[0]),
					"slot_type":           toString(row[1]),
					"active":              active,
					"restart_lsn":         toString(row[3]),
					"confirmed_flush_lsn": toString(row[4]),
					"wal_status":          toString(row[5]),
				}
				slots = append(slots, slot)

				if !active {
					warnings = append(warnings, fmt.Sprintf("Replication slot '%s' is inactive — this can cause WAL retention bloat", toString(row[0])))
				}
				walStatus := toString(row[5])
				if walStatus == "lost" {
					warnings = append(warnings, fmt.Sprintf("Replication slot '%s' has WAL status 'lost' — the replica using this slot will need to be re-initialized", toString(row[0])))
				}
			}
			resp["replication_slots"] = slots
		} else {
			resp["replication_slots"] = []any{}
		}

		// 3. Replication stat (shows connected replicas on primary).
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
					warnings = append(warnings, fmt.Sprintf("Replica %s has replay lag of %ds — investigate if the replica is overloaded or network is slow", toString(row[0]), replayLag))
				}
			}
			resp["connected_replicas"] = replicas
		} else {
			resp["connected_replicas"] = []any{}
		}

		// 4. If this is a replica, show lag from primary.
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

				resp["replica_status"] = map[string]any{
					"replay_lag_bytes":     lagBytes,
					"last_received_lsn":    toString(row[1]),
					"last_replayed_lsn":    toString(row[2]),
					"seconds_behind_primary": secondsBehind,
				}

				if secondsBehind > 60 {
					warnings = append(warnings, fmt.Sprintf("This replica is %d seconds behind the primary", secondsBehind))
				}
				if lagBytes > 100*1024*1024 { // 100MB
					warnings = append(warnings, fmt.Sprintf("Replay lag is %d bytes — the replica may be struggling to keep up", lagBytes))
				}
			}
		}

		// 5. WAL archival status (if configured).
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
			archivedCount := toInt64(row[0])
			failedCount := toInt64(row[3])

			archive := map[string]any{
				"archived_count":     archivedCount,
				"last_archived_wal":  toString(row[1]),
				"last_archived_time": toString(row[2]),
				"failed_count":       failedCount,
			}
			if toString(row[4]) != "" {
				archive["last_failed_wal"] = toString(row[4])
				archive["last_failed_time"] = toString(row[5])
			}
			resp["wal_archival"] = archive

			if failedCount > 0 {
				warnings = append(warnings, fmt.Sprintf("WAL archiver has %d failed archives — check archive_command configuration and disk space", failedCount))
			}
		}

		if len(warnings) > 0 {
			resp["warnings"] = warnings
		}

		data, err := json.Marshal(resp)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
