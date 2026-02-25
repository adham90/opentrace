package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/connector"
)

// dbLocksHandler returns a handler that shows current lock contention in the
// database: blocking chains, lock types, and waiting queries.
func dbLocksHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active. Connect a PostgreSQL data source first."), nil
		}

		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("The active database connector does not support direct queries."), nil
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
