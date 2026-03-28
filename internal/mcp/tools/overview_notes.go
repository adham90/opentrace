package tools

import (
	"context"
	"fmt"
	"time"
)

// --- notes action (agent memory) ---

func HandleOverviewNotes(ctx context.Context, d OverviewDeps, args map[string]any) (*CallToolResult, error) {
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

		resp := map[string]any{
			"entity_type": result.EntityType,
			"entity_id":   result.EntityID,
			"note":        result.Note,
			"updated_at":  result.UpdatedAt.Format(time.RFC3339),
			"message":     fmt.Sprintf("Note saved for %s '%s'. This will be included in future tool responses.", entityType, entityID),
		}
		return JSONResult(resp)
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
		return EmptyResult("No agent notes found. Use overview with action=notes and a note parameter to save context for future sessions.")
	}

	resp := map[string]any{
		"count": len(notes),
		"notes": notes,
	}
	return JSONResult(resp)
}

func HandleOverviewDeleteNote(ctx context.Context, d OverviewDeps, args map[string]any) (*CallToolResult, error) {
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

	resp := map[string]any{
		"status":  "deleted",
		"message": fmt.Sprintf("Note for %s '%s' deleted.", entityType, entityID),
	}
	return JSONResult(resp)
}

// --- settings action (read-only) ---

func HandleOverviewSettings(ctx context.Context, d OverviewDeps) (*CallToolResult, error) {
	if d.SettingsStore == nil {
		return NewToolResultError("SettingsStore not configured"), nil
	}

	resp := map[string]any{}

	if retention, err := d.SettingsStore.GetRetention(ctx); err == nil {
		resp["retention_days"] = retention.RetentionDays
		resp["metric_retention_days"] = retention.MetricRetentionDays
	}
	if v, err := d.SettingsStore.GetAPIKey(ctx); err == nil && v != "" {
		resp["api_key"] = v
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
