package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/store"
)

// listConnectorsHandler returns a handler that lists connectors.
// When a DataSourceStore is available, returns full connector details from the
// database with optional type filter. Falls back to listing
// active registry tools when no store is provided.
func listConnectorsHandler(registry *connector.Registry, dsStore store.DataSourceStore) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := GetArguments(request)

		// When we have a store, return rich connector info.
		if dsStore != nil {
			var params store.ListDataSourceParams
			if v, ok := args["type"].(string); ok && v != "" {
				params.Type = store.ConnectorType(v)
			}

			connectors, err := dsStore.List(ctx, params)
			if err != nil {
				return NewToolResultError(fmt.Sprintf("failed to list connectors: %v", err)), nil
			}
			if len(connectors) == 0 {
				return NewToolResultText("No connectors found."), nil
			}

			// Build response with connector details + active tools.
			type connectorEntry struct {
				ID            string   `json:"id"`
				Name          string   `json:"name"`
				Type          string   `json:"type"`
				Status        string   `json:"status"`
				StatusMessage string   `json:"status_message,omitempty"`
				LastTestedAt  string   `json:"last_tested_at,omitempty"`
				ActiveTools   []string `json:"active_tools,omitempty"`
			}

			// Collect active tool names for reference.
			activeToolNames := make([]string, 0)
			for _, t := range registry.AllTools() {
				activeToolNames = append(activeToolNames, t.Name)
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
				// Include active tools if this connector is connected.
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
		tools := registry.AllTools()
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
}

// listServersHandler returns a handler that lists all monitored servers.
func listServersHandler(ss store.ServerStore) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		servers, err := ss.List(ctx, store.ListServerParams{})
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to list servers: %v", err)), nil
		}
		if len(servers) == 0 {
			return NewToolResultText("No monitored servers."), nil
		}
		data, err := json.Marshal(servers)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to marshal servers: %v", err)), nil
		}
		return NewToolResultText(string(data)), nil
	}
}

// queryMetricsHandler returns a handler that queries time-series metrics.
func queryMetricsHandler(ss store.ServerStore, ms store.MetricStore) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := GetArguments(request)

		serverIDStr, _ := args["server_id"].(string)
		if serverIDStr == "" {
			return NewToolResultError("server_id is required"), nil
		}

		serverID, err := uuid.Parse(serverIDStr)
		if err != nil {
			return NewToolResultError("invalid server_id format"), nil
		}

		q := store.MetricQuery{ServerID: serverID}
		if v, ok := args["metric_name"].(string); ok {
			q.MetricName = v
		}
		if v, ok := args["start"].(string); ok && v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				q.Start = &t
			}
		}
		if v, ok := args["end"].(string); ok && v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				q.End = &t
			}
		}
		if v, ok := args["limit"].(float64); ok && v > 0 {
			q.Limit = int(v)
		}

		points, err := ms.Query(ctx, q)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to query metrics: %v", err)), nil
		}
		if len(points) == 0 {
			return NewToolResultText("No metrics found matching the given criteria."), nil
		}

		data, err := json.Marshal(points)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to marshal metrics: %v", err)), nil
		}
		return NewToolResultText(string(data)), nil
	}
}

// serverHealthHandler returns a handler that shows the latest metrics for a server.
func serverHealthHandler(ss store.ServerStore, ms store.MetricStore) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := GetArguments(request)

		serverIDStr, _ := args["server_id"].(string)
		if serverIDStr == "" {
			return NewToolResultError("server_id is required"), nil
		}

		serverID, err := uuid.Parse(serverIDStr)
		if err != nil {
			return NewToolResultError("invalid server_id format"), nil
		}

		srv, err := ss.GetByID(ctx, serverID)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("server not found: %v", err)), nil
		}

		latest, err := ms.LatestByServer(ctx, serverID)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to query metrics: %v", err)), nil
		}

		result := map[string]any{
			"server":  srv,
			"metrics": latest,
		}
		data, err := json.Marshal(result)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return NewToolResultText(string(data)), nil
	}
}
