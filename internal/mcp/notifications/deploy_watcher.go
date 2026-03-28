package notifications

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// DeployWatcher monitors error rates after deploys and notifies if they spike.
type DeployWatcher struct {
	dispatcher      *Dispatcher
	errorGroupStore store.ErrorGroupStore
	logStore        store.LogStore
}

// NewDeployWatcher creates a deploy watcher.
func NewDeployWatcher(d *Dispatcher, egs store.ErrorGroupStore, ls store.LogStore) *DeployWatcher {
	return &DeployWatcher{
		dispatcher:      d,
		errorGroupStore: egs,
		logStore:        ls,
	}
}

// OnDeploy starts a background observation window after a deploy event.
// It monitors error rate for 30 minutes and notifies if it spikes >50% above baseline.
func (w *DeployWatcher) OnDeploy(ctx context.Context, deploy store.Deploy) {
	if w.logStore == nil || w.dispatcher == nil {
		return
	}

	// Snapshot pre-deploy error rate (last 30 min before deploy)
	preWindow := 30 * time.Minute
	baseline := w.countErrors(ctx, deploy.Service, deploy.DeployedAt.Add(-preWindow), deploy.DeployedAt)

	slog.Info("deploy watcher: starting observation",
		"deploy_id", deploy.ID,
		"service", deploy.Service,
		"baseline_errors", baseline,
	)

	go w.observe(ctx, deploy, baseline)
}

func (w *DeployWatcher) observe(ctx context.Context, deploy store.Deploy, baseline int) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	timeout := time.After(30 * time.Minute)
	alerted := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			slog.Info("deploy watcher: observation window ended, all clear",
				"deploy_id", deploy.ID,
				"service", deploy.Service,
			)
			return
		case <-ticker.C:
			current := w.countErrors(ctx, deploy.Service, deploy.DeployedAt, time.Now())

			// Only alert if baseline had some traffic and current is significantly worse
			threshold := baseline + 3 // at least 3 more errors than baseline
			if baseline > 0 {
				threshold = int(float64(baseline) * 1.5) // 50% increase
			}

			if current > threshold && !alerted {
				alerted = true

				// Get recent error groups for context
				var errorDetails []map[string]any
				if w.errorGroupStore != nil {
					groups, err := w.errorGroupStore.List(ctx, store.ListErrorGroupParams{
						Service: deploy.Service,
						Status:  store.ErrorGroupUnresolved,
						Limit:   3,
						SortBy:  "last_seen_at",
					})
					if err == nil {
						for _, g := range groups {
							errorDetails = append(errorDetails, map[string]any{
								"fingerprint":     g.Fingerprint,
								"exception_class": g.ExceptionClass,
								"message":         g.Message,
								"count":           g.OccurrenceCount,
							})
						}
					}
				}

				commitShort := deploy.CommitHash
				if len(commitShort) > 8 {
					commitShort = commitShort[:8]
				}

				w.dispatcher.Notify(Notification{
					Type:     NotifyErrorSpikeAfterDeploy,
					Severity: "critical",
					Title:    fmt.Sprintf("Error spike after deploy to %s", deploy.Service),
					Summary: fmt.Sprintf(
						"Error count increased from %d to %d (baseline 30min before deploy vs %s since deploy). Deploy commit: %s by %s.",
						baseline, current, time.Since(deploy.DeployedAt).Round(time.Minute), commitShort, deploy.Author,
					),
					Context: map[string]any{
						"deploy_id":       deploy.ID,
						"service":         deploy.Service,
						"commit":          deploy.CommitHash,
						"author":          deploy.Author,
						"branch":          deploy.Branch,
						"deployed_at":     deploy.DeployedAt.Format(time.RFC3339),
						"baseline_errors": baseline,
						"current_errors":  current,
						"top_errors":      errorDetails,
						"suggested_action": "Investigate the new errors. Check if they correlate with the deploy diff. Consider rolling back if user impact is high.",
					},
				})
				return // Only alert once per deploy
			}
		}
	}
}

// countErrors counts error-level logs for a service in a time window.
func (w *DeployWatcher) countErrors(ctx context.Context, service string, since, until time.Time) int {
	if w.logStore == nil {
		return 0
	}
	counts, err := w.logStore.CountByLevel(ctx, store.LogCountParams{
		Service: service,
		Since:   since,
		Until:   until,
	})
	if err != nil {
		return 0
	}
	total := 0
	for level, count := range counts {
		if level == "ERROR" || level == "FATAL" {
			total += count
		}
	}
	return total
}
