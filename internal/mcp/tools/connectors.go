package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/store"
)

// ConnectorsDeps holds the stores needed by the connectors tool.
type ConnectorsDeps struct {
	DSStore       store.DataSourceStore
	Registry      *connector.Registry
	LogStore      store.LogStore
	Config        *config.Config
	SettingsStore store.SettingsStore
}

// ConnectorsHandler returns a handler for the consolidated connectors tool.
func ConnectorsHandler(d ConnectorsDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action, _ := args["action"].(string)

		switch action {
		case "list":
			return handleConnectorList(ctx, d, args)
		case "get":
			return handleConnectorGet(ctx, d, args)
		case "create":
			return handleConnectorCreate(ctx, d, args)
		case "test":
			return handleConnectorTest(ctx, d, args)
		case "update":
			return handleConnectorUpdate(ctx, d, args)
		case "delete":
			return handleConnectorDelete(ctx, d, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use list, get, create, test, update, delete)", action)), nil
		}
	}
}

func handleConnectorList(ctx context.Context, d ConnectorsDeps, args map[string]any) (*CallToolResult, error) {
	if d.DSStore != nil {
		var params store.ListDataSourceParams
		if v, ok := args["type"].(string); ok && v != "" {
			params.Type = store.ConnectorType(v)
		}

		connectors, err := d.DSStore.List(ctx, params)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to list connectors: %v", err)), nil
		}
		if len(connectors) == 0 {
			return NewToolResultText("No connectors found."), nil
		}

		type connectorEntry struct {
			ID            string   `json:"id"`
			Name          string   `json:"name"`
			Type          string   `json:"type"`
			Status        string   `json:"status"`
			StatusMessage string   `json:"status_message,omitempty"`
			LastTestedAt  string   `json:"last_tested_at,omitempty"`
			ActiveTools   []string `json:"active_tools,omitempty"`
		}

		activeToolNames := make([]string, 0)
		if d.Registry != nil {
			for _, t := range d.Registry.AllTools() {
				activeToolNames = append(activeToolNames, t.Name)
			}
		}

		entries := make([]connectorEntry, 0, len(connectors))
		for _, c := range connectors {
			e := connectorEntry{
				ID:     c.ID.String(),
				Name:   c.Name,
				Type:   string(c.Type),
				Status: string(c.Status),
			}
			if c.StatusMessage != nil {
				e.StatusMessage = *c.StatusMessage
			}
			if c.LastTestedAt != nil {
				e.LastTestedAt = c.LastTestedAt.Format(time.RFC3339)
			}
			if c.Status == store.StatusConnected {
				e.ActiveTools = activeToolNames
			}
			entries = append(entries, e)
		}

		data, err := json.Marshal(entries)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to marshal connectors: %v", err)), nil
		}
		return NewToolResultText(string(data)), nil
	}

	// Fallback: no store, just list active registry tools.
	if d.Registry == nil {
		return NewToolResultText("No connectors are currently active."), nil
	}
	tools := d.Registry.AllTools()
	if len(tools) == 0 {
		return NewToolResultText("No connectors are currently active."), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Active tools (%d):\n", len(tools)))
	for _, t := range tools {
		b.WriteString(fmt.Sprintf("- %s: %s\n", t.Name, t.Description))
	}

	return NewToolResultText(b.String()), nil
}

func handleConnectorGet(ctx context.Context, d ConnectorsDeps, args map[string]any) (*CallToolResult, error) {
	if d.DSStore == nil {
		return NewToolResultError("DataSourceStore not configured"), nil
	}

	idStr, _ := args["connector_id"].(string)
	if idStr == "" {
		return NewToolResultError("connector_id is required"), nil
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		return NewToolResultError("invalid connector_id format"), nil
	}

	ds, err := d.DSStore.GetByID(ctx, id)
	if err != nil {
		if err == store.ErrNotFound {
			return NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
		}
		return NewToolResultError(fmt.Sprintf("failed to fetch connector: %v", err)), nil
	}

	data, err := json.Marshal(ds)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal connector: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

func handleConnectorCreate(ctx context.Context, d ConnectorsDeps, args map[string]any) (*CallToolResult, error) {
	if d.DSStore == nil {
		return NewToolResultError("DataSourceStore not configured"), nil
	}

	name, _ := args["name"].(string)
	if name == "" {
		return NewToolResultError("name is required"), nil
	}

	dsType, _ := args["connector_type"].(string)
	if dsType == "" {
		return NewToolResultError("connector_type is required (database, mysql, redis, turso, or logs)"), nil
	}
	validTypes := map[string]bool{
		"database": true, "mysql": true, "redis": true, "turso": true, "logs": true,
	}
	if !validTypes[dsType] {
		return NewToolResultError("connector_type must be one of: database, mysql, redis, turso, logs"), nil
	}

	needsConnStr := map[string]bool{
		"database": true, "mysql": true, "redis": true, "turso": true,
	}

	cfg := make(map[string]any)
	if connStr, ok := args["connection_string"].(string); ok && connStr != "" {
		cfg["connection_string"] = connStr
	} else if needsConnStr[dsType] {
		return NewToolResultError(fmt.Sprintf("connection_string is required for %s connectors", dsType)), nil
	}

	if dsType == "turso" {
		if authToken, ok := args["auth_token"].(string); ok && authToken != "" {
			cfg["auth_token"] = authToken
		}
	}

	ds, err := d.DSStore.Create(ctx, store.CreateDataSourceParams{
		Type:   store.ConnectorType(dsType),
		Name:   name,
		Config: cfg,
	})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to create connector: %v", err)), nil
	}

	return NewToolResultText(fmt.Sprintf("Connector %q created (id: %s, type: %s, status: %s). Use connectors with action=test to verify the connection.", ds.Name, ds.ID, ds.Type, ds.Status)), nil
}

func handleConnectorTest(ctx context.Context, d ConnectorsDeps, args map[string]any) (*CallToolResult, error) {
	if d.DSStore == nil {
		return NewToolResultError("DataSourceStore not configured"), nil
	}

	idStr, _ := args["connector_id"].(string)
	if idStr == "" {
		return NewToolResultError("connector_id is required"), nil
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		return NewToolResultError("invalid connector_id format"), nil
	}

	ds, err := d.DSStore.GetByID(ctx, id)
	if err != nil {
		if err == store.ErrNotFound {
			return NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
		}
		return NewToolResultError(fmt.Sprintf("failed to fetch connector: %v", err)), nil
	}

	c, err := connector.CreateConnector(ctx, *ds, d.LogStore, d.Config, d.SettingsStore)
	if err != nil {
		status := store.StatusError
		msg := fmt.Sprintf("failed to create connector: %v", err)
		d.DSStore.Update(ctx, id, store.UpdateDataSourceParams{
			Status: &status, StatusMessage: &msg,
		})
		return NewToolResultError(fmt.Sprintf("failed to create connector: %v", err)), nil
	}

	if err := c.TestConnection(ctx); err != nil {
		c.Close()
		status := store.StatusError
		msg := fmt.Sprintf("connection test failed: %v", err)
		now := time.Now()
		d.DSStore.Update(ctx, id, store.UpdateDataSourceParams{
			Status: &status, StatusMessage: &msg, LastTestedAt: &now,
		})
		return NewToolResultError(fmt.Sprintf("connection test failed: %v", err)), nil
	}

	if d.Registry != nil {
		d.Registry.Register(c)
	}

	status := store.StatusConnected
	now := time.Now()
	d.DSStore.Update(ctx, id, store.UpdateDataSourceParams{
		Status: &status, LastTestedAt: &now,
	})

	return NewToolResultText(fmt.Sprintf("Connector %q (%s) tested and connected successfully.", ds.Name, ds.Type)), nil
}

func handleConnectorUpdate(ctx context.Context, d ConnectorsDeps, args map[string]any) (*CallToolResult, error) {
	if d.DSStore == nil {
		return NewToolResultError("DataSourceStore not configured"), nil
	}

	idStr, _ := args["connector_id"].(string)
	if idStr == "" {
		return NewToolResultError("connector_id is required"), nil
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		return NewToolResultError("invalid connector_id format"), nil
	}

	ds, err := d.DSStore.GetByID(ctx, id)
	if err != nil {
		if err == store.ErrNotFound {
			return NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
		}
		return NewToolResultError(fmt.Sprintf("failed to fetch connector: %v", err)), nil
	}

	var params store.UpdateDataSourceParams

	if v, ok := args["name"].(string); ok && v != "" {
		params.Name = &v
	}

	configChanged := false
	if connStr, ok := args["connection_string"].(string); ok && connStr != "" {
		params.Config = map[string]any{"connection_string": connStr}
		configChanged = true
	}

	if configChanged && d.Registry != nil {
		d.Registry.Unregister(connector.ConnectorType(string(ds.Type)))
	}

	updated, err := d.DSStore.Update(ctx, id, params)
	if err != nil {
		if err == store.ErrNotFound {
			return NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
		}
		return NewToolResultError(fmt.Sprintf("failed to update connector: %v", err)), nil
	}

	msg := fmt.Sprintf("Connector %q updated successfully.", updated.Name)
	if configChanged {
		msg += " Connection config changed — use connectors with action=test to re-establish the connection."
	}

	return NewToolResultText(msg), nil
}

func handleConnectorDelete(ctx context.Context, d ConnectorsDeps, args map[string]any) (*CallToolResult, error) {
	if d.DSStore == nil {
		return NewToolResultError("DataSourceStore not configured"), nil
	}

	idStr, _ := args["connector_id"].(string)
	if idStr == "" {
		return NewToolResultError("connector_id is required"), nil
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		return NewToolResultError("invalid connector_id format"), nil
	}

	ds, err := d.DSStore.GetByID(ctx, id)
	if err != nil {
		if err == store.ErrNotFound {
			return NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
		}
		return NewToolResultError(fmt.Sprintf("failed to fetch connector: %v", err)), nil
	}

	if d.Registry != nil {
		d.Registry.Unregister(connector.ConnectorType(string(ds.Type)))
	}

	if err := d.DSStore.Delete(ctx, id); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to delete connector: %v", err)), nil
	}

	return NewToolResultText(fmt.Sprintf("Connector %q (%s) deleted and disconnected.", ds.Name, ds.Type)), nil
}
