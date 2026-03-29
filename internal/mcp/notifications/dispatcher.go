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

// Dispatcher watches for events and dispatches notifications to all registered senders
// (MCP clients, Telegram, Slack, webhooks, etc.).
type Dispatcher struct {
	senders []Sender
}

// NewDispatcher creates a notification dispatcher with one or more senders.
// Pass an MCPServerSender, a NotificationBridge for Telegram, etc.
func NewDispatcher(senders ...Sender) *Dispatcher {
	var active []Sender
	for _, s := range senders {
		if s != nil {
			active = append(active, s)
		}
	}
	return &Dispatcher{senders: active}
}

// AddSender adds a sender at runtime (e.g., when Telegram is configured after startup).
func (d *Dispatcher) AddSender(s Sender) {
	if s != nil {
		d.senders = append(d.senders, s)
	}
}

// Notify sends a notification to all registered senders.
func (d *Dispatcher) Notify(n Notification) {
	if len(d.senders) == 0 {
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
		"senders", len(d.senders),
	)

	for _, s := range d.senders {
		s.SendNotificationToAllClients(MCPMethod, params)
	}
}
