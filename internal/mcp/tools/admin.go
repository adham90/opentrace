package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"


	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/store"
)

// SessionSummaryHandler is a callback for handling session summary updates.
// This avoids the tools package depending on the mcp package's sessionTracker.
type SessionSummaryHandler func(ctx context.Context, args map[string]any) (*CallToolResult, error)

// AdminDeps holds the stores needed by the admin tool.
type AdminDeps struct {
	SettingsStore          store.SettingsStore
	UserStore              store.UserStore
	AuditStore             store.AuditStore
	AgentNoteStore         store.AgentNoteStore
	MCPActivityStore       store.MCPActivityStore
	Registry               *connector.Registry
	SessionSummaryCallback SessionSummaryHandler // optional, nil-safe
}

// AdminHandler returns a handler for the consolidated admin tool.
func AdminHandler(d AdminDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action, _ := args["action"].(string)

		switch action {
		case "settings":
			return HandleAdminSettings(ctx, d)
		case "update_retention":
			return HandleUpdateRetention(ctx, d, args)
		case "users":
			return HandleListUsers(ctx, d)
		case "update_role":
			return HandleUpdateRole(ctx, d, args)
		case "toggle_active":
			return HandleToggleActive(ctx, d, args)
		case "delete_user":
			return HandleDeleteUser(ctx, d, args)
		case "audit":
			return HandleAudit(ctx, d, args)
		case "notes":
			return HandleNotes(ctx, d, args)
		case "delete_note":
			return HandleDeleteNote(ctx, d, args)
		case "activity":
			return HandleAdminActivity(ctx, d)
		case "session_summary":
			if d.SessionSummaryCallback == nil {
				return NewToolResultError("session tracking is not enabled"), nil
			}
			return d.SessionSummaryCallback(ctx, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use settings, update_retention, users, update_role, toggle_active, delete_user, audit, notes, delete_note, activity, session_summary)", action)), nil
		}
	}
}

func HandleAdminSettings(ctx context.Context, d AdminDeps) (*CallToolResult, error) {
	if d.SettingsStore == nil {
		return NewToolResultError("SettingsStore not configured"), nil
	}

	retention, err := d.SettingsStore.GetRetention(ctx)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to read retention settings: %v", err)), nil
	}

	resp := map[string]any{
		"retention_days":        retention.RetentionDays,
		"metric_retention_days": retention.MetricRetentionDays,
	}

	if v, err := d.SettingsStore.GetMaxQueryRows(ctx); err == nil && v > 0 {
		resp["max_query_rows"] = v
	}
	if v, err := d.SettingsStore.GetStatementTimeout(ctx); err == nil && v > 0 {
		resp["statement_timeout_ms"] = v
	}
	if v, err := d.SettingsStore.GetMCPName(ctx); err == nil && v != "" {
		resp["mcp_name"] = v
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal settings: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

func HandleUpdateRetention(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.SettingsStore == nil {
		return NewToolResultError("SettingsStore not configured"), nil
	}

	daysF, ok := args["retention_days"].(float64)
	if !ok || daysF < 1 || daysF > 365 {
		return NewToolResultError("retention_days is required and must be between 1 and 365"), nil
	}
	days := int(daysF)

	if err := d.SettingsStore.SetRetention(ctx, store.RetentionSettings{RetentionDays: days}); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to update retention: %v", err)), nil
	}

	return NewToolResultText(fmt.Sprintf("Data retention updated to %d days. Logs, alerts, and watcher runs older than %d days will be pruned on the next cleanup cycle.", days, days)), nil
}

func HandleListUsers(ctx context.Context, d AdminDeps) (*CallToolResult, error) {
	if d.UserStore == nil {
		return NewToolResultError("UserStore not configured"), nil
	}

	users, err := d.UserStore.List(ctx)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list users: %v", err)), nil
	}

	type userSummary struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		IsActive    bool   `json:"is_active"`
		MCPEnabled  bool   `json:"mcp_enabled"`
		CreatedAt   string `json:"created_at"`
	}

	summaries := make([]userSummary, len(users))
	for i, u := range users {
		summaries[i] = userSummary{
			ID:          u.ID,
			Email:       u.Email,
			DisplayName: u.DisplayName,
			Role:        string(u.Role),
			IsActive:    u.IsActive,
			MCPEnabled:  u.MCPEnabled,
			CreatedAt:   u.CreatedAt.Format("2006-01-02 15:04:05"),
		}
	}

	data, err := json.Marshal(summaries)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal users: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

func HandleUpdateRole(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.UserStore == nil {
		return NewToolResultError("UserStore not configured"), nil
	}

	userID, _ := args["user_id"].(string)
	if userID == "" {
		return NewToolResultError("user_id is required. Use admin with action=users to find user IDs."), nil
	}

	roleStr, _ := args["role"].(string)
	if roleStr != "admin" && roleStr != "member" {
		return NewToolResultError("role must be 'admin' or 'member'."), nil
	}

	role := store.UserRole(roleStr)
	user, err := d.UserStore.Update(ctx, userID, store.UpdateUserParams{Role: &role})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to update role: %v", err)), nil
	}

	result := map[string]any{
		"status":  "updated",
		"user_id": user.ID,
		"email":   user.Email,
		"role":    string(user.Role),
	}

	data, err := json.Marshal(result)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

func HandleToggleActive(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.UserStore == nil {
		return NewToolResultError("UserStore not configured"), nil
	}

	userID, _ := args["user_id"].(string)
	if userID == "" {
		return NewToolResultError("user_id is required. Use admin with action=users to find user IDs."), nil
	}

	active, ok := args["is_active"].(bool)
	if !ok {
		return NewToolResultError("is_active is required (true or false)."), nil
	}

	user, err := d.UserStore.Update(ctx, userID, store.UpdateUserParams{IsActive: &active})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to update user: %v", err)), nil
	}

	status := "enabled"
	if !user.IsActive {
		status = "disabled"
	}

	result := map[string]any{
		"status":    status,
		"user_id":   user.ID,
		"email":     user.Email,
		"is_active": user.IsActive,
	}

	data, err := json.Marshal(result)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

func HandleDeleteUser(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.UserStore == nil {
		return NewToolResultError("UserStore not configured"), nil
	}

	userID, _ := args["user_id"].(string)
	if userID == "" {
		return NewToolResultError("user_id is required. Use admin with action=users to find user IDs."), nil
	}

	user, err := d.UserStore.GetByID(ctx, userID)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("user not found: %v", err)), nil
	}

	if err := d.UserStore.Delete(ctx, userID); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to delete user: %v", err)), nil
	}

	result := map[string]any{
		"status":  "deleted",
		"user_id": userID,
		"email":   user.Email,
	}

	data, err := json.Marshal(result)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

func HandleAudit(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.AuditStore == nil {
		return NewToolResultError("AuditStore not configured"), nil
	}

	limit := 50
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	if limit > 200 {
		limit = 200
	}

	entries, err := d.AuditStore.Recent(ctx, limit)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to fetch audit log: %v", err)), nil
	}

	if len(entries) == 0 {
		return NewToolResultText("No audit log entries found."), nil
	}

	data, err := json.Marshal(entries)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal audit log: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

func HandleNotes(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.AgentNoteStore == nil {
		return NewToolResultError("AgentNoteStore not configured"), nil
	}

	entityType, _ := args["entity_type"].(string)
	entityID, _ := args["entity_id"].(string)
	noteText, _ := args["note"].(string)

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

		resp := map[string]any{
			"entity_type": result.EntityType,
			"entity_id":   result.EntityID,
			"note":        result.Note,
			"updated_at":  result.UpdatedAt.Format(time.RFC3339),
			"message":     fmt.Sprintf("Note saved for %s '%s'. This will be included in future tool responses.", entityType, entityID),
		}

		data, _ := json.Marshal(resp)
		return NewToolResultText(string(data)), nil
	}

	// If both type and ID given, get a specific note
	if entityType != "" && entityID != "" {
		note, err := d.AgentNoteStore.Get(ctx, entityType, entityID)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("note not found: %v", err)), nil
		}
		data, _ := json.Marshal(note)
		return NewToolResultText(string(data)), nil
	}

	// Otherwise, list (optionally filtered by entity_type)
	notes, err := d.AgentNoteStore.List(ctx, entityType)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list notes: %v", err)), nil
	}

	if len(notes) == 0 {
		return NewToolResultText("No agent notes found. Use admin with action=notes and a note parameter to save context for future sessions."), nil
	}

	resp := map[string]any{
		"count": len(notes),
		"notes": notes,
	}
	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

func HandleDeleteNote(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.AgentNoteStore == nil {
		return NewToolResultError("AgentNoteStore not configured"), nil
	}

	entityType, _ := args["entity_type"].(string)
	if entityType == "" {
		return NewToolResultError("entity_type is required"), nil
	}
	entityID, _ := args["entity_id"].(string)
	if entityID == "" {
		return NewToolResultError("entity_id is required"), nil
	}

	if err := d.AgentNoteStore.Delete(ctx, entityType, entityID); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to delete note: %v", err)), nil
	}

	resp := map[string]any{
		"status":  "deleted",
		"message": fmt.Sprintf("Note for %s '%s' deleted.", entityType, entityID),
	}
	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
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

	data, err := json.Marshal(resp)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}
