package tools

import (
	"context"
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
		action := ArgString(args, "action")

		switch action {
		case "list":
			return HandleListServers(ctx, d)
		case "query":
			return HandleQueryMetrics(ctx, d, args)
		case "health":
			return HandleServerHealth(ctx, d, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use list, query, health)", action)), nil
		}
	}
}

func HandleListServers(ctx context.Context, d ServersDeps) (*CallToolResult, error) {
	servers, err := d.ServerStore.List(ctx, store.ListServerParams{})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list servers: %v", err)), nil
	}
	if len(servers) == 0 {
		return EmptyResult("No monitored servers.")
	}
	return JSONResult(servers)
}

func HandleQueryMetrics(ctx context.Context, d ServersDeps, args map[string]any) (*CallToolResult, error) {
	serverIDStr := ArgString(args, "server_id")
	if serverIDStr == "" {
		return NewToolResultError("server_id is required"), nil
	}

	serverID, err := uuid.Parse(serverIDStr)
	if err != nil {
		return NewToolResultError("invalid server_id format"), nil
	}

	q := store.MetricQuery{ServerID: serverID}
	q.MetricName = ArgString(args, "metric_name")
	if v := ArgString(args, "start"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.Start = &t
		}
	}
	if v := ArgString(args, "end"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.End = &t
		}
	}
	q.Limit = ArgInt(args, "limit", 0, 1000)

	points, err := d.MetricStore.Query(ctx, q)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query metrics: %v", err)), nil
	}
	if len(points) == 0 {
		return EmptyResult("No metrics found matching the given criteria.")
	}

	return JSONResult(points)
}

func HandleServerHealth(ctx context.Context, d ServersDeps, args map[string]any) (*CallToolResult, error) {
	serverIDStr := ArgString(args, "server_id")
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

	return JSONResult(map[string]any{
		"server":  srv,
		"metrics": latest,
	})
}
