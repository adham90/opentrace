package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/connector"
)

// checkpointStatsHandler returns a handler that shows WAL/checkpoint health.
func checkpointStatsHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active."), nil
		}
		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("active database connector does not support direct queries"), nil
		}

		// pg_stat_bgwriter has checkpoint info.
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
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("pg_stat_bgwriter query failed: %v", err)), nil
		}

		resp := map[string]any{}
		var warnings []string

		if len(bgResult.Rows) > 0 && len(bgResult.Rows[0]) >= 10 {
			row := bgResult.Rows[0]
			timedF, _ := toFloat64(row[0])
			reqF, _ := toFloat64(row[1])
			total := timedF + reqF

			var reqPct float64
			if total > 0 {
				reqPct = 100.0 * reqF / total
			}

			resp["checkpoints"] = map[string]any{
				"timed":             row[0],
				"requested":        row[1],
				"total":            total,
				"requested_pct":    fmt.Sprintf("%.1f%%", reqPct),
				"write_time_ms":    row[2],
				"sync_time_ms":     row[3],
			}
			resp["buffers"] = map[string]any{
				"checkpoint":    row[4],
				"clean":         row[5],
				"backend":       row[6],
				"backend_fsync": row[7],
				"alloc":         row[8],
			}
			resp["stats_reset"] = row[9]

			if reqPct > 50 {
				warnings = append(warnings, fmt.Sprintf("%.0f%% of checkpoints are requested (not timed) — increase max_wal_size or checkpoint_completion_target", reqPct))
			}

			backendF, _ := toFloat64(row[6])
			checkpointF, _ := toFloat64(row[4])
			if checkpointF > 0 && backendF/checkpointF > 0.1 {
				warnings = append(warnings, "High backend buffer writes — increase shared_buffers so the bgwriter handles more writes")
			}
		}

		// Try pg_stat_wal for WAL generation rate (PG14+).
		walQuery := `SELECT wal_records, wal_fpi, wal_bytes,
			pg_size_pretty(wal_bytes::bigint) AS wal_size, stats_reset
		FROM pg_stat_wal`
		walResult, err := qe.ExecuteReadQuery(ctx, walQuery)
		if err == nil && len(walResult.Rows) > 0 && len(walResult.Rows[0]) >= 5 {
			row := walResult.Rows[0]
			resp["wal"] = map[string]any{
				"records":       row[0],
				"full_page_images": row[1],
				"total_bytes":   row[2],
				"total_size":    row[3],
				"stats_reset":   row[4],
			}
		}

		// Current WAL position.
		walPosQuery := `SELECT pg_current_wal_lsn()::text, pg_walfile_name(pg_current_wal_lsn())`
		walPosResult, err := qe.ExecuteReadQuery(ctx, walPosQuery)
		if err == nil && len(walPosResult.Rows) > 0 && len(walPosResult.Rows[0]) >= 2 {
			resp["wal_position"] = map[string]any{
				"lsn":  walPosResult.Rows[0][0],
				"file": walPosResult.Rows[0][1],
			}
		}

		resp["warnings"] = warnings

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
