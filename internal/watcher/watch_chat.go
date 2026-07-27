package watcher

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// messageSender delivers a plain notification message to a chat channel.
// *notify.TelegramSender and *notify.SlackSender both satisfy this, so the
// watcher package can bridge to them without importing internal/notify.
type messageSender interface {
	Send(ctx context.Context, message string) error
}

// ChatWatchNotifier adapts a messageSender to the WatchAlertNotifier interface
// by rendering the alert as an HTML message (the Slack sender converts that to
// mrkdwn on its way out). The underlying sender is expected to silently skip
// delivery when its channel is not configured, so including this notifier
// unconditionally is safe.
type ChatWatchNotifier struct {
	sender messageSender
}

// NewChatWatchNotifier wraps a message sender as a watch-alert notifier.
// A nil sender yields a notifier that no-ops (never panics), so callers can
// wire it in even when the channel is unconfigured.
func NewChatWatchNotifier(sender messageSender) *ChatWatchNotifier {
	return &ChatWatchNotifier{sender: sender}
}

// NotifyWatchAlert renders and delivers the alert. Safe for concurrent use.
func (n *ChatWatchNotifier) NotifyWatchAlert(ctx context.Context, alert *store.WatchAlert, watch *store.Watch) error {
	if n == nil || n.sender == nil {
		return nil
	}
	return n.sender.Send(ctx, formatWatchAlertMessage(alert, watch))
}

// formatWatchAlertMessage renders a watch alert as a chat-friendly HTML message.
func formatWatchAlertMessage(alert *store.WatchAlert, watch *store.Watch) string {
	env := alert.Environment
	service := ""
	if watch != nil {
		if env == "" {
			env = watch.Environment
		}
		service = string(watch.Service)
	}
	return fmt.Sprintf(
		"🚨 <b>Watch alert</b>\n"+
			"<b>Metric:</b> %s\n"+
			"<b>Value:</b> %.4g (threshold %.4g)\n"+
			"<b>Urgency:</b> %s\n"+
			"<b>Service:</b> %s\n"+
			"<b>Environment:</b> %s\n"+
			"%s\n"+
			"<i>%s</i>",
		alert.TriggerMetric(),
		alert.TriggerValue(),
		alert.ThresholdValue(),
		alert.Urgency,
		service,
		env,
		alert.Summary,
		alert.CreatedAt.Format(time.RFC3339),
	)
}
