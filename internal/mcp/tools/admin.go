package tools

import (
	"context"
	"fmt"

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
		action := ArgString(args, "action")

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
		case "notifications":
			return HandleNotifications(ctx, d, args)
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

	return JSONResult(resp)
}

// Retention bounds, in days. SetRetention persists the whole settings struct,
// so anything outside this range would be written verbatim into the pruner's
// cutoff calculation.
const (
	minRetentionDays = 1
	maxRetentionDays = 365
)

// HandleUpdateRetention changes the global retention window.
//
// It reads the current settings first and only replaces RetentionDays.
// SetRetention marshals the entire RetentionSettings struct and upserts it, so
// constructing a fresh struct here wrote MetricRetentionDays back as 0 — a lost
// update that silently reverted a separately configured metric retention policy
// to "follow the global window".
func HandleUpdateRetention(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.SettingsStore == nil {
		return NewToolResultError("SettingsStore not configured"), nil
	}

	// MCP numeric args arrive as float64.
	daysF, ok := args["retention_days"].(float64)
	if !ok || daysF < minRetentionDays || daysF > maxRetentionDays {
		return NewToolResultError(fmt.Sprintf("retention_days is required and must be between %d and %d", minRetentionDays, maxRetentionDays)), nil
	}
	days := int(daysF)

	current, err := d.SettingsStore.GetRetention(ctx)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to read current retention settings: %v", err)), nil
	}

	updated := *current
	updated.RetentionDays = days

	if err := d.SettingsStore.SetRetention(ctx, updated); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to update retention: %v", err)), nil
	}

	// Be precise about what this number governs. It drives the pruner in
	// cmd/opentrace/background_jobs.go: logs, MCP activity, the audit log,
	// error groups, agent notes, code entities and healthcheck results. Watch
	// runs and watch alerts are pruned from the separate `retention_policy`
	// config blob (internal/jobs/retention.go) and are NOT affected, which the
	// old message claimed they were.
	msg := fmt.Sprintf(
		"Data retention updated to %d days. Logs, MCP activity, audit entries, error groups, agent notes, code entities and healthcheck results older than %d days will be pruned on the next cleanup cycle. "+
			"Watch runs and watch alerts follow the separate retention_policy config, not this setting.",
		days, days)
	if updated.MetricRetentionDays > 0 {
		msg += fmt.Sprintf(" Metric buckets keep their own %d-day window.", updated.MetricRetentionDays)
	} else {
		msg += " Metric buckets have no separate window, so they follow this one."
	}
	return NewToolResultText(msg), nil
}
