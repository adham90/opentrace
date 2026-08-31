package oncall

import (
	"context"
	"log/slog"

	"github.com/adham90/opentrace/internal/healthcheck"
	"github.com/adham90/opentrace/pkg/store"
)

// WatchNotifier adapts the runner to watcher.WatchAlertNotifier, so on-call
// triage slots in beside the log, chat, and webhook notifiers rather than
// needing its own path out of the watcher.
type WatchNotifier struct {
	Runner *Runner
}

// NotifyWatchAlert runs the agent for a watch alert.
//
// It never returns an error to the dispatcher. The on-call agent is an
// enhancement on top of delivery, and a failed diagnosis must not look like a
// failed alert — the chat and webhook notifiers have already fired by now.
func (n *WatchNotifier) NotifyWatchAlert(ctx context.Context, alert *store.WatchAlert, watch *store.Watch) error {
	if n.Runner == nil || !n.Runner.Enabled() {
		return nil
	}
	if _, err := n.Runner.Run(ctx, PayloadFromWatch(alert, watch)); err != nil && err != ErrDisabled {
		slog.Error("on-call triage failed", "error", err, "alert_id", alertID(alert))
	}
	return nil
}

// HealthCheckNotifier adapts the runner to healthcheck.HealthCheckAlertNotifier.
type HealthCheckNotifier struct {
	Runner *Runner
}

// NotifyHealthCheckAlert runs the agent when an endpoint changes state.
//
// Only transitions into a non-up state are triaged: "it came back" is good news
// and does not need a diagnosis, and running the agent on recovery is how a
// flapping endpoint burns a day's quota by lunchtime.
func (n *HealthCheckNotifier) NotifyHealthCheckAlert(ctx context.Context, alert *healthcheck.HealthCheckAlert) error {
	if n.Runner == nil || !n.Runner.Enabled() || alert == nil {
		return nil
	}
	if alert.CurrentStatus == store.HealthCheckUp {
		return nil
	}
	if _, err := n.Runner.Run(ctx, PayloadFromHealthCheck(alert)); err != nil && err != ErrDisabled {
		slog.Error("on-call triage failed", "error", err, "healthcheck_id", alert.HealthCheckID)
	}
	return nil
}

func alertID(a *store.WatchAlert) string {
	if a == nil {
		return ""
	}
	return a.ID
}
