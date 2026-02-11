package digest

// DeriveStatus computes the overall digest status from alert and watcher summaries.
// Priority: degraded > critical > needs_attention > info_only > healthy.
func DeriveStatus(alerts AlertSummary, watchers WatcherSummary) DigestStatus {
	if watchers.InError > 0 {
		return StatusDegraded
	}
	if alerts.Critical > 0 {
		return StatusCritical
	}
	if alerts.Warning > 0 {
		return StatusNeedsAttention
	}
	if alerts.Info > 0 {
		return StatusInfoOnly
	}
	return StatusHealthy
}
