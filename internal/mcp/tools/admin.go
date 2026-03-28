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
