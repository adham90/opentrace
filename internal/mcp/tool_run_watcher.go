package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// runWatcherNowHandler returns a handler that triggers an immediate watcher execution.
func runWatcherNowHandler(ws store.WatcherStore, executor *watcher.Executor) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		idStr, _ := args["watcher_id"].(string)
		if idStr == "" {
			return mcp.NewToolResultError("watcher_id is required"), nil
		}

		id, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("invalid watcher_id format"), nil
		}

		w, err := ws.GetByID(ctx, id)
		if err != nil {
			if err == store.ErrNotFound {
				return mcp.NewToolResultError(fmt.Sprintf("watcher %s not found", idStr)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("failed to fetch watcher: %v", err)), nil
		}

		// Execute asynchronously — return immediately.
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic in watcher MCP run", "watcher_id", w.ID, "error", r)
				}
			}()
			executor.Execute(context.Background(), *w)
		}()

		return mcp.NewToolResultText(fmt.Sprintf("Watcher %q (%s) triggered for immediate execution.", w.Title, w.ID)), nil
	}
}
