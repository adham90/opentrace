package tools

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/adham90/opentrace/internal/connector"
)

func HandleAudit(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.AuditStore == nil {
		return NewToolResultError("AuditStore not configured"), nil
	}

	limit := ArgInt(args, "limit", 50, 200)

	entries, err := d.AuditStore.Recent(ctx, limit)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to fetch audit log: %v", err)), nil
	}

	if len(entries) == 0 {
		return EmptyResult("No audit log entries found.")
	}

	return JSONResult(entries)
}

func HandleNotes(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.AgentNoteStore == nil {
		return NewToolResultError("AgentNoteStore not configured"), nil
	}

	entityType := ArgString(args, "entity_type")
	entityID := ArgString(args, "entity_id")
	noteText := ArgString(args, "note")

	// If note text is provided, this is an upsert
	if noteText != "" {
		if entityType == "" {
			return NewToolResultError("entity_type is required (query, endpoint, service, healthcheck, error)"), nil
		}
		if entityID == "" {
			return NewToolResultError("entity_id is required"), nil
		}

		result, err := d.AgentNoteStore.Upsert(ctx, entityType, entityID, noteText)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to save note: %v", err)), nil
		}

		return JSONResult(map[string]any{
			"entity_type": result.EntityType,
			"entity_id":   result.EntityID,
			"note":        result.Note,
			"updated_at":  result.UpdatedAt.Format(time.RFC3339),
			"message":     fmt.Sprintf("Note saved for %s '%s'. This will be included in future tool responses.", entityType, entityID),
		})
	}

	// If both type and ID given, get a specific note
	if entityType != "" && entityID != "" {
		note, err := d.AgentNoteStore.Get(ctx, entityType, entityID)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("note not found: %v", err)), nil
		}
		return JSONResult(note)
	}

	// Otherwise, list (optionally filtered by entity_type)
	notes, err := d.AgentNoteStore.List(ctx, entityType)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list notes: %v", err)), nil
	}

	if len(notes) == 0 {
		return EmptyResult("No agent notes found. Use admin with action=notes and a note parameter to save context for future sessions.")
	}

	return JSONResult(map[string]any{
		"count": len(notes),
		"notes": notes,
	})
}

func HandleDeleteNote(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.AgentNoteStore == nil {
		return NewToolResultError("AgentNoteStore not configured"), nil
	}

	entityType := ArgString(args, "entity_type")
	if entityType == "" {
		return NewToolResultError("entity_type is required"), nil
	}
	entityID := ArgString(args, "entity_id")
	if entityID == "" {
		return NewToolResultError("entity_id is required"), nil
	}

	if err := d.AgentNoteStore.Delete(ctx, entityType, entityID); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to delete note: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"status":  "deleted",
		"message": fmt.Sprintf("Note for %s '%s' deleted.", entityType, entityID),
	})
}

func HandleAdminActivity(ctx context.Context, d AdminDeps) (*CallToolResult, error) {
	if d.Registry == nil {
		return NewToolResultError("No connector registry available."), nil
	}

	ds := d.Registry.Get(connector.ConnectorDatabase)
	if ds == nil {
		return NewToolResultError("No database connector is active. Connect a PostgreSQL data source first."), nil
	}

	qe, ok := ds.(connector.QueryExecutor)
	if !ok {
		return NewToolResultError("The active database connector does not support direct queries."), nil
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

	totalResult, err := qe.ExecuteReadQuery(ctx, "SELECT count(*) FROM pg_stat_activity")
	if err == nil && totalResult.RowCount > 0 && len(totalResult.Rows[0]) > 0 {
		if v, vErr := strconv.Atoi(fmt.Sprintf("%v", totalResult.Rows[0][0])); vErr == nil {
			totalConns = v
		}
	}

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
		"total_connections":    totalConns,
		"connection_summary":   summaries,
		"long_running_queries": longQueries,
		"idle_in_transaction":  idleSessions,
		"warnings":             warnings,
	}
	if maxConns > 0 {
		resp["max_connections"] = maxConns
		resp["utilization_percent"] = float64(totalConns) / float64(maxConns) * 100
	}

	return JSONResult(resp)
}
