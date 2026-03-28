package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/pkg/store"
)

// ServersDeps holds the stores needed by the consolidated servers tool.
type ServersDeps struct {
	ServerStore store.ServerStore
	MetricStore store.MetricStore
}

// ServersHandler returns a handler for the consolidated servers tool.
func ServersHandler(d ServersDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action, _ := args["action"].(string)

		switch action {
		case "list":
			return handleListServers(ctx, d)
		case "query":
			return handleQueryMetrics(ctx, d, args)
		case "health":
			return handleServerHealth(ctx, d, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use list, query, health)", action)), nil
		}
	}
}

func handleListServers(ctx context.Context, d ServersDeps) (*CallToolResult, error) {
	servers, err := d.ServerStore.List(ctx, store.ListServerParams{})
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

func handleQueryMetrics(ctx context.Context, d ServersDeps, args map[string]any) (*CallToolResult, error) {
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

	points, err := d.MetricStore.Query(ctx, q)
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

func handleServerHealth(ctx context.Context, d ServersDeps, args map[string]any) (*CallToolResult, error) {
	serverIDStr, _ := args["server_id"].(string)
	if serverIDStr == "" {
		return NewToolResultError("server_id is required"), nil
	}

	serverID, err := uuid.Parse(serverIDStr)
	if err != nil {
		return NewToolResultError("invalid server_id format"), nil
	}

	srv, err := d.ServerStore.GetByID(ctx, serverID)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("server not found: %v", err)), nil
	}

	latest, err := d.MetricStore.LatestByServer(ctx, serverID)
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
