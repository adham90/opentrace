package notifications

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Sender is the interface for sending MCP notifications.
type Sender interface {
	SendNotificationToAllClients(method string, params map[string]any)
}

// MCPServerSender wraps an *mcp.Server to implement the Sender interface
// by iterating over all active sessions and sending a log message to each.
type MCPServerSender struct {
	Server *mcp.Server
}

// NewMCPServerSender creates a new MCPServerSender wrapping the given server.
func NewMCPServerSender(server *mcp.Server) *MCPServerSender {
	return &MCPServerSender{Server: server}
}

// SendNotificationToAllClients sends a notification to all connected MCP sessions
// using the logging mechanism. The method name is passed as the Logger field so
// clients can distinguish OpenTrace alerts from regular log messages.
func (s *MCPServerSender) SendNotificationToAllClients(method string, params map[string]any) {
	if s.Server == nil {
		return
	}
	ctx := context.Background()
	for ss := range s.Server.Sessions() {
		if err := ss.Log(ctx, &mcp.LoggingMessageParams{
			Level:  "info",
			Logger: method,
			Data:   params,
		}); err != nil {
			slog.Debug("failed to send notification to session", "error", err)
		}
	}
}

// Compile-time check that *MCPServerSender satisfies Sender.
var _ Sender = (*MCPServerSender)(nil)

// Dispatcher watches for events and dispatches notifications to connected MCP clients.
type Dispatcher struct {
	sender Sender
}

// NewDispatcher creates a notification dispatcher.
// Pass an MCPServerSender (or any Sender) as the sender.
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
