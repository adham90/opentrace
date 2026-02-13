package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// bulkWatcherTool returns the tool definition for bulk_watcher_update.
func bulkWatcherTool() mcp.Tool {
	return mcp.NewTool("bulk_watcher_update",
		mcp.WithDescription("Update multiple watchers at once. Apply status, schedule, or severity changes to watchers matching the given filters. At least one filter and one update field are required."),
		mcp.WithString("filter_status", mcp.Description("Filter watchers by current status: active or paused")),
		mcp.WithString("filter_watcher_type", mcp.Description("Filter watchers by type: ai or rule")),
		mcp.WithString("set_status", mcp.Description("New status to apply: active or paused")),
		mcp.WithString("set_schedule", mcp.Description("New schedule to apply (cron expression or interval like '5m', '@hourly')")),
		mcp.WithString("set_severity", mcp.Description("New severity to apply: info, warning, or critical")),
	)
}

// bulkWatcherHandler returns a handler that updates multiple watchers matching the given filters.
func bulkWatcherHandler(ws store.WatcherStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		// Parse filter fields.
		filterStatus, _ := args["filter_status"].(string)
		filterType, _ := args["filter_watcher_type"].(string)

		// Parse update fields.
		setStatus, _ := args["set_status"].(string)
		setSchedule, _ := args["set_schedule"].(string)
		setSeverity, _ := args["set_severity"].(string)

		// Validate at least one filter is provided.
		hasFilter := filterStatus != "" || filterType != ""
		if !hasFilter {
			return mcp.NewToolResultError("at least one filter is required: filter_status or filter_watcher_type"), nil
		}

		// Validate at least one update field is provided.
		hasUpdate := setStatus != "" || setSchedule != "" || setSeverity != ""
		if !hasUpdate {
			return mcp.NewToolResultError("at least one update field is required: set_status, set_schedule, or set_severity"), nil
		}

		// Validate set_status value.
		if setStatus != "" && setStatus != "active" && setStatus != "paused" {
			return mcp.NewToolResultError("set_status must be 'active' or 'paused'"), nil
		}

		// List watchers with watcher_type filter via ListWatcherParams.
		listParams := store.ListWatcherParams{}
		if filterType != "" {
			listParams.WatcherType = store.WatcherType(filterType)
		}

		watchers, err := ws.List(ctx, listParams)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list watchers: %v", err)), nil
		}

		// Post-filter by status (not supported in ListWatcherParams).
		if filterStatus != "" {
			filtered := make([]store.Watcher, 0, len(watchers))
			for _, w := range watchers {
				if string(w.Status) == filterStatus {
					filtered = append(filtered, w)
				}
			}
			watchers = filtered
		}

		if len(watchers) == 0 {
			return mcp.NewToolResultText("No watchers matched the given filters. Nothing to update."), nil
		}

		// Apply updates to each matching watcher.
		var updatedIDs []string
		var errors []string

		for _, w := range watchers {
			// Apply status change.
			if setStatus != "" {
				_, err := ws.UpdateStatus(ctx, w.ID, store.WatcherStatus(setStatus))
				if err != nil {
					errors = append(errors, fmt.Sprintf("%s (%s): status update failed: %v", w.ID, w.Title, err))
					continue
				}
			}

			// Apply schedule and/or severity changes via Update.
			if setSchedule != "" || setSeverity != "" {
				params := store.UpdateWatcherParams{}
				if setSchedule != "" {
					params.Schedule = &setSchedule
				}
				if setSeverity != "" {
					sev := store.WatcherSeverity(setSeverity)
					params.Severity = &sev
				}
				_, err := ws.Update(ctx, w.ID, params)
				if err != nil {
					errors = append(errors, fmt.Sprintf("%s (%s): update failed: %v", w.ID, w.Title, err))
					continue
				}
			}

			updatedIDs = append(updatedIDs, w.ID.String())
		}

		result := struct {
			Matched int      `json:"matched"`
			Updated int      `json:"updated"`
			IDs     []string `json:"updated_ids"`
			Errors  []string `json:"errors,omitempty"`
		}{
			Matched: len(watchers),
			Updated: len(updatedIDs),
			IDs:     updatedIDs,
			Errors:  errors,
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Bulk update complete. %d of %d watchers updated.\n%s", len(updatedIDs), len(watchers), string(data))), nil
	}
}
