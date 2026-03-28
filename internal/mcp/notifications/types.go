// Package notifications provides proactive alert dispatching to connected MCP clients.
// Instead of the agent polling for problems, OpenTrace pushes notifications
// when it detects error spikes, new error groups, health check failures, or watcher triggers.
package notifications

// NotificationType identifies the kind of notification.
type NotificationType string

const (
	// NotifyErrorSpikeAfterDeploy fires when error rate increases >50% within 30 minutes of a deploy.
	NotifyErrorSpikeAfterDeploy NotificationType = "error_spike_after_deploy"

	// NotifyNewErrorGroup fires when a new error fingerprint appears 3+ times in 10 minutes.
	NotifyNewErrorGroup NotificationType = "new_error_group"

	// NotifyHealthCheckDown fires when a health check transitions from UP to DOWN.
	NotifyHealthCheckDown NotificationType = "health_check_down"

	// NotifyWatcherTriggered fires when a watcher rule fires.
	NotifyWatcherTriggered NotificationType = "watcher_triggered"

	// NotifyDeployVerified fires when an error group is resolved after a fix deploy.
	NotifyDeployVerified NotificationType = "deploy_verified"

	// NotifyDeployFixFailed fires when an error persists after a fix deploy.
	NotifyDeployFixFailed NotificationType = "deploy_fix_failed"
)

// Notification is the payload sent to connected MCP clients.
type Notification struct {
	Type     NotificationType       `json:"type"`
	Severity string                 `json:"severity"` // "critical", "error", "warn", "info"
	Title    string                 `json:"title"`
	Summary  string                 `json:"summary"`
	Context  map[string]any         `json:"context,omitempty"`
}

// MCPMethod is the MCP notification method name used for all OpenTrace alerts.
const MCPMethod = "notifications/opentrace/alert"
