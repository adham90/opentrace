package notifications

import (
	"log/slog"

	"github.com/mark3labs/mcp-go/server"
)

// Sender is the interface for sending MCP notifications.
// Implemented by *server.MCPServer.
type Sender interface {
	SendNotificationToAllClients(method string, params map[string]any)
}

// Dispatcher watches for events and dispatches notifications to connected MCP clients.
type Dispatcher struct {
	sender Sender
}

// NewDispatcher creates a notification dispatcher.
// Pass the MCPServer from the SSE setup as the sender.
func NewDispatcher(sender Sender) *Dispatcher {
	return &Dispatcher{sender: sender}
}

// Notify sends a notification to all connected MCP clients.
func (d *Dispatcher) Notify(n Notification) {
	if d.sender == nil {
		return
	}

	params := map[string]any{
		"type":     string(n.Type),
		"severity": n.Severity,
		"title":    n.Title,
		"summary":  n.Summary,
	}
	if n.Context != nil {
		params["context"] = n.Context
	}

	slog.Info("dispatching notification",
		"type", n.Type,
		"severity", n.Severity,
		"title", n.Title,
	)

	d.sender.SendNotificationToAllClients(MCPMethod, params)
}

// Compile-time check that *server.MCPServer satisfies Sender.
var _ Sender = (*server.MCPServer)(nil)
