package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// addNoteHandler creates or updates a note attached to an entity.
func addNoteHandler(ns store.AgentNoteStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if ns == nil {
			return mcp.NewToolResultError("AgentNoteStore not configured"), nil
		}

		args := request.GetArguments()
		entityType, _ := args["entity_type"].(string)
		if entityType == "" {
			return mcp.NewToolResultError("entity_type is required (query, endpoint, service, healthcheck, error)"), nil
		}
		entityID, _ := args["entity_id"].(string)
		if entityID == "" {
			return mcp.NewToolResultError("entity_id is required"), nil
		}
		note, _ := args["note"].(string)
		if note == "" {
			return mcp.NewToolResultError("note is required"), nil
		}

		result, err := ns.Upsert(ctx, entityType, entityID, note)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to save note: %v", err)), nil
		}

		resp := map[string]any{
			"entity_type": result.EntityType,
			"entity_id":   result.EntityID,
			"note":        result.Note,
			"updated_at":  result.UpdatedAt.Format(time.RFC3339),
			"message":     fmt.Sprintf("Note saved for %s '%s'. This will be included in future tool responses.", entityType, entityID),
		}

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// getNotesHandler reads notes for an entity or lists all notes.
func getNotesHandler(ns store.AgentNoteStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if ns == nil {
			return mcp.NewToolResultError("AgentNoteStore not configured"), nil
		}

		args := request.GetArguments()
		entityType, _ := args["entity_type"].(string)
		entityID, _ := args["entity_id"].(string)

		// If both type and ID given, get a specific note
		if entityType != "" && entityID != "" {
			note, err := ns.Get(ctx, entityType, entityID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("note not found: %v", err)), nil
			}
			data, _ := json.Marshal(note)
			return mcp.NewToolResultText(string(data)), nil
		}

		// Otherwise, list (optionally filtered by entity_type)
		notes, err := ns.List(ctx, entityType)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list notes: %v", err)), nil
		}

		if len(notes) == 0 {
			return mcp.NewToolResultText("No agent notes found. Use add_note to save context for future sessions."), nil
		}

		resp := map[string]any{
			"count": len(notes),
			"notes": notes,
		}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// deleteNoteHandler removes a note.
func deleteNoteHandler(ns store.AgentNoteStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if ns == nil {
			return mcp.NewToolResultError("AgentNoteStore not configured"), nil
		}

		args := request.GetArguments()
		entityType, _ := args["entity_type"].(string)
		if entityType == "" {
			return mcp.NewToolResultError("entity_type is required"), nil
		}
		entityID, _ := args["entity_id"].(string)
		if entityID == "" {
			return mcp.NewToolResultError("entity_id is required"), nil
		}

		if err := ns.Delete(ctx, entityType, entityID); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to delete note: %v", err)), nil
		}

		resp := map[string]any{
			"status":  "deleted",
			"message": fmt.Sprintf("Note for %s '%s' deleted.", entityType, entityID),
		}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}
