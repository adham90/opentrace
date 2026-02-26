package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// createConnectorHandler returns a handler that creates a new data source connector.
func createConnectorHandler(dsStore store.DataSourceStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		name, _ := args["name"].(string)
		if name == "" {
			return mcp.NewToolResultError("name is required"), nil
		}

		dsType, _ := args["type"].(string)
		if dsType == "" {
			return mcp.NewToolResultError("type is required (database, mysql, redis, turso, or logs)"), nil
		}
		validTypes := map[string]bool{
			"database": true, "mysql": true, "redis": true, "turso": true, "logs": true,
		}
		if !validTypes[dsType] {
			return mcp.NewToolResultError("type must be one of: database, mysql, redis, turso, logs"), nil
		}

		needsConnStr := map[string]bool{
			"database": true, "mysql": true, "redis": true, "turso": true,
		}

		cfg := make(map[string]any)
		if connStr, ok := args["connection_string"].(string); ok && connStr != "" {
			cfg["connection_string"] = connStr
		} else if needsConnStr[dsType] {
			return mcp.NewToolResultError(fmt.Sprintf("connection_string is required for %s connectors", dsType)), nil
		}

		// Turso also accepts auth_token
		if dsType == "turso" {
			if authToken, ok := args["auth_token"].(string); ok && authToken != "" {
				cfg["auth_token"] = authToken
			}
		}

		ds, err := dsStore.Create(ctx, store.CreateDataSourceParams{
			Type:   store.ConnectorType(dsType),
			Name:   name,
			Config: cfg,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create connector: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Connector %q created (id: %s, type: %s, status: %s). Use test_connector to verify the connection.", ds.Name, ds.ID, ds.Type, ds.Status)), nil
	}
}

// testConnectorHandler returns a handler that tests an existing connector.
func testConnectorHandler(dsStore store.DataSourceStore, registry *connector.Registry, logStore store.LogStore, cfg *config.Config, ss store.SettingsStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		idStr, _ := args["connector_id"].(string)
		if idStr == "" {
			return mcp.NewToolResultError("connector_id is required"), nil
		}

		id, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("invalid connector_id format"), nil
		}

		ds, err := dsStore.GetByID(ctx, id)
		if err != nil {
			if err == store.ErrNotFound {
				return mcp.NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("failed to fetch connector: %v", err)), nil
		}

		// Create and test the connector.
		c, err := connector.CreateConnector(ctx, *ds, logStore, cfg, ss)
		if err != nil {
			status := store.StatusError
			msg := fmt.Sprintf("failed to create connector: %v", err)
			dsStore.Update(ctx, id, store.UpdateDataSourceParams{
				Status: &status, StatusMessage: &msg,
			})
			return mcp.NewToolResultError(fmt.Sprintf("failed to create connector: %v", err)), nil
		}

		if err := c.TestConnection(ctx); err != nil {
			c.Close()
			status := store.StatusError
			msg := fmt.Sprintf("connection test failed: %v", err)
			now := time.Now()
			dsStore.Update(ctx, id, store.UpdateDataSourceParams{
				Status: &status, StatusMessage: &msg, LastTestedAt: &now,
			})
			return mcp.NewToolResultError(fmt.Sprintf("connection test failed: %v", err)), nil
		}

		// Register the connector.
		registry.Register(c)

		status := store.StatusConnected
		now := time.Now()
		dsStore.Update(ctx, id, store.UpdateDataSourceParams{
			Status: &status, LastTestedAt: &now,
		})

		return mcp.NewToolResultText(fmt.Sprintf("Connector %q (%s) tested and connected successfully.", ds.Name, ds.Type)), nil
	}
}

// deleteConnectorHandler returns a handler that deletes a connector.
func deleteConnectorHandler(dsStore store.DataSourceStore, registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		idStr, _ := args["connector_id"].(string)
		if idStr == "" {
			return mcp.NewToolResultError("connector_id is required"), nil
		}

		id, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("invalid connector_id format"), nil
		}

		ds, err := dsStore.GetByID(ctx, id)
		if err != nil {
			if err == store.ErrNotFound {
				return mcp.NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("failed to fetch connector: %v", err)), nil
		}

		// Unregister from registry (closes the connection).
		// connector.ConnectorType and store.ConnectorType use the same string values.
		registry.Unregister(connector.ConnectorType(string(ds.Type)))

		// Delete from store.
		if err := dsStore.Delete(ctx, id); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to delete connector: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Connector %q (%s) deleted and disconnected.", ds.Name, ds.Type)), nil
	}
}

// getConnectorHandler returns a handler that retrieves a single connector by ID.
func getConnectorHandler(dsStore store.DataSourceStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		idStr, _ := args["connector_id"].(string)
		if idStr == "" {
			return mcp.NewToolResultError("connector_id is required"), nil
		}

		id, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("invalid connector_id format"), nil
		}

		ds, err := dsStore.GetByID(ctx, id)
		if err != nil {
			if err == store.ErrNotFound {
				return mcp.NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("failed to fetch connector: %v", err)), nil
		}

		data, err := json.Marshal(ds)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal connector: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

// updateConnectorHandler returns a handler that updates an existing connector.
func updateConnectorHandler(dsStore store.DataSourceStore, registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		idStr, _ := args["connector_id"].(string)
		if idStr == "" {
			return mcp.NewToolResultError("connector_id is required"), nil
		}

		id, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("invalid connector_id format"), nil
		}

		// Check the connector exists.
		ds, err := dsStore.GetByID(ctx, id)
		if err != nil {
			if err == store.ErrNotFound {
				return mcp.NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("failed to fetch connector: %v", err)), nil
		}

		var params store.UpdateDataSourceParams

		if v, ok := args["name"].(string); ok && v != "" {
			params.Name = &v
		}

		// Build config from connection_string if provided.
		configChanged := false
		if connStr, ok := args["connection_string"].(string); ok && connStr != "" {
			params.Config = map[string]any{"connection_string": connStr}
			configChanged = true
		}

		// Unregister old connector from registry when config changes.
		if configChanged && registry != nil {
			registry.Unregister(connector.ConnectorType(string(ds.Type)))
		}

		updated, err := dsStore.Update(ctx, id, params)
		if err != nil {
			if err == store.ErrNotFound {
				return mcp.NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("failed to update connector: %v", err)), nil
		}

		msg := fmt.Sprintf("Connector %q updated successfully.", updated.Name)
		if configChanged {
			msg += " Connection config changed — use test_connector to re-establish the connection."
		}

		return mcp.NewToolResultText(msg), nil
	}
}
