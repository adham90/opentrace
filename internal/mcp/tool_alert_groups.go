package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// listIncidentsTool returns the tool definition for list_incidents.
func listIncidentsTool() mcp.Tool {
	return mcp.NewTool("list_incidents",
		mcp.WithDescription("List alert groups (incidents). Groups cluster related alerts together for incident management. Use to see active incidents, their severity, and how many alerts each contains."),
		mcp.WithString("status", mcp.Description("Filter by status: open, investigating, resolved (omit for all)")),
	)
}

// listIncidentsHandler returns a handler that lists alert groups.
func listIncidentsHandler(ags store.AlertGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		status, _ := args["status"].(string)

		groups, err := ags.List(ctx, status)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list incidents: %v", err)), nil
		}
		if len(groups) == 0 {
			return mcp.NewToolResultText("No incidents found."), nil
		}

		data, err := json.MarshalIndent(groups, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal incidents: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// getIncidentTool returns the tool definition for get_incident.
func getIncidentTool() mcp.Tool {
	return mcp.NewTool("get_incident",
		mcp.WithDescription("Get full details for an incident (alert group), including all member alerts, root cause, and resolution notes."),
		mcp.WithString("incident_id", mcp.Required(), mcp.Description("Incident UUID (from list_incidents)")),
	)
}

// getIncidentHandler returns a handler that gets an incident by ID.
func getIncidentHandler(ags store.AlertGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		id, _ := args["incident_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("incident_id is required. Use list_incidents to find IDs."), nil
		}

		group, err := ags.GetByID(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("incident not found: %v", err)), nil
		}

		data, err := json.MarshalIndent(group, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal incident: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// createIncidentTool returns the tool definition for create_incident.
func createIncidentTool() mcp.Tool {
	return mcp.NewTool("create_incident",
		mcp.WithDescription("Create a new incident (alert group) to cluster related alerts together. Provide a title and optionally attach existing alert IDs."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Incident title (e.g. 'Database connection spike 2024-01-15')")),
		mcp.WithString("severity", mcp.Description("Severity: info, warning, critical (default: warning)")),
		mcp.WithString("alert_ids", mcp.Description("Comma-separated alert IDs to attach to this incident")),
	)
}

// createIncidentHandler returns a handler that creates an incident.
func createIncidentHandler(ags store.AlertGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		title, _ := args["title"].(string)
		if title == "" {
			return mcp.NewToolResultError("title is required"), nil
		}

		severity, _ := args["severity"].(string)
		if severity == "" {
			severity = "warning"
		}

		var alertIDs []string
		if idsStr, ok := args["alert_ids"].(string); ok && idsStr != "" {
			alertIDs = splitAndTrim(idsStr)
		}

		group, err := ags.Create(ctx, store.CreateAlertGroupParams{
			Title:    title,
			Severity: severity,
			AlertIDs: alertIDs,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create incident: %v", err)), nil
		}

		data, err := json.MarshalIndent(group, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal incident: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Incident created:\n%s", string(data))), nil
	}
}

// updateIncidentTool returns the tool definition for update_incident.
func updateIncidentTool() mcp.Tool {
	return mcp.NewTool("update_incident",
		mcp.WithDescription("Update an incident's status, severity, root cause, or resolution. Use to track incident lifecycle: open → investigating → resolved."),
		mcp.WithString("incident_id", mcp.Required(), mcp.Description("Incident UUID (from list_incidents)")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("status", mcp.Description("New status: open, investigating, resolved")),
		mcp.WithString("severity", mcp.Description("New severity: info, warning, critical")),
		mcp.WithString("root_cause", mcp.Description("Root cause analysis text")),
		mcp.WithString("resolution", mcp.Description("Resolution steps taken")),
	)
}

// updateIncidentHandler returns a handler that updates an incident.
func updateIncidentHandler(ags store.AlertGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		id, _ := args["incident_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("incident_id is required. Use list_incidents to find IDs."), nil
		}

		params := store.UpdateAlertGroupParams{}
		if v, ok := args["title"].(string); ok && v != "" {
			params.Title = &v
		}
		if v, ok := args["status"].(string); ok && v != "" {
			params.Status = &v
		}
		if v, ok := args["severity"].(string); ok && v != "" {
			params.Severity = &v
		}
		if v, ok := args["root_cause"].(string); ok && v != "" {
			params.RootCause = &v
		}
		if v, ok := args["resolution"].(string); ok && v != "" {
			params.Resolution = &v
		}

		if err := ags.Update(ctx, id, params); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to update incident: %v", err)), nil
		}

		// Return the updated incident.
		group, err := ags.GetByID(ctx, id)
		if err != nil {
			return mcp.NewToolResultText("Incident updated successfully."), nil
		}

		data, err := json.MarshalIndent(group, "", "  ")
		if err != nil {
			return mcp.NewToolResultText("Incident updated successfully."), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Incident updated:\n%s", string(data))), nil
	}
}

// addAlertsToIncidentTool returns the tool definition.
func addAlertsToIncidentTool() mcp.Tool {
	return mcp.NewTool("add_alerts_to_incident",
		mcp.WithDescription("Add one or more alerts to an existing incident. Use when you discover additional related alerts after creating an incident."),
		mcp.WithString("incident_id", mcp.Required(), mcp.Description("Incident UUID")),
		mcp.WithString("alert_ids", mcp.Required(), mcp.Description("Comma-separated alert IDs to add")),
	)
}

// addAlertsToIncidentHandler returns a handler that adds alerts to an incident.
func addAlertsToIncidentHandler(ags store.AlertGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		id, _ := args["incident_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("incident_id is required"), nil
		}

		idsStr, _ := args["alert_ids"].(string)
		if idsStr == "" {
			return mcp.NewToolResultError("alert_ids is required"), nil
		}

		alertIDs := splitAndTrim(idsStr)
		if err := ags.AddAlerts(ctx, id, alertIDs); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to add alerts: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Added %d alert(s) to incident %s.", len(alertIDs), id)), nil
	}
}

// autoCorrelateAlertsTool returns the tool definition.
func autoCorrelateAlertsTool() mcp.Tool {
	return mcp.NewTool("auto_correlate_alerts",
		mcp.WithDescription("Automatically group unclustered alerts that fired within a time window from different watchers. Uses union-find to build connected components. Returns the newly created incident groups."),
		mcp.WithNumber("window_minutes", mcp.Description("Time window in minutes for correlation (default: 10)")),
	)
}

// autoCorrelateAlertsHandler returns a handler that auto-correlates alerts.
func autoCorrelateAlertsHandler(ags store.AlertGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		windowMinutes := 10
		if v, ok := args["window_minutes"].(float64); ok && v > 0 {
			windowMinutes = int(v)
		}

		groups, err := ags.AutoCorrelate(ctx, windowMinutes)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to auto-correlate: %v", err)), nil
		}

		if len(groups) == 0 {
			return mcp.NewToolResultText("No correlated alert groups found. All alerts are either already grouped or too far apart in time."), nil
		}

		data, err := json.MarshalIndent(groups, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal groups: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Created %d incident group(s):\n%s", len(groups), string(data))), nil
	}
}

// deleteIncidentTool returns the tool definition.
func deleteIncidentTool() mcp.Tool {
	return mcp.NewTool("delete_incident",
		mcp.WithDescription("Delete an incident (alert group). The member alerts are NOT deleted — they remain as individual alerts."),
		mcp.WithString("incident_id", mcp.Required(), mcp.Description("Incident UUID (from list_incidents)")),
	)
}

// deleteIncidentHandler returns a handler that deletes an incident.
func deleteIncidentHandler(ags store.AlertGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		id, _ := args["incident_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("incident_id is required. Use list_incidents to find IDs."), nil
		}

		if err := ags.Delete(ctx, id); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to delete incident: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Incident %s deleted. Member alerts are preserved.", id)), nil
	}
}

// splitAndTrim splits a comma-separated string and trims whitespace.
func splitAndTrim(s string) []string {
	var result []string
	for _, part := range splitComma(s) {
		trimmed := trimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitComma(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
