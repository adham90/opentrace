package watcher

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// WatchSessionManager handles auto-resolve and cleanup of watches.
type WatchSessionManager struct {
	watchStore store.WatchStore
	metrics    *WatchMetrics
}

// NewWatchSessionManager creates a new session manager.
func NewWatchSessionManager(watchStore store.WatchStore, metrics *WatchMetrics) *WatchSessionManager {
	return &WatchSessionManager{
		watchStore: watchStore,
		metrics:    metrics,
	}
}

// CheckAutoResolve checks if a triggered watch should auto-resolve because the
// metric has returned to within 10% of the baseline value.
func (m *WatchSessionManager) CheckAutoResolve(ctx context.Context, w *store.Watch) error {
	if w.Status != store.WatchStatusTriggered {
		return nil
	}
	if w.BaselineJSON == nil {
		return nil
	}

	window, err := time.ParseDuration(w.BaselineWindow)
	if err != nil {
		window = 1 * time.Hour
	}

	// Extract the primary metric from the conditions tree for baseline comparison.
	metric := store.ConditionsMetric(w.ConditionsJSON)
	if metric == "" {
		// Compound condition: re-evaluate the full tree. If it no longer breaches,
		// auto-resolve.
		cond, err := ParseCondition(w.ConditionsJSON)
		if err != nil {
			return err
		}
		result, err := EvaluateCondition(ctx, cond, m.metrics, w.BaselineJSON, w.Environment, window)
		if err != nil {
			return err
		}
		if !result.Breached {
			slog.Info("auto-resolving watch (compound condition no longer breached)",
				"watch_id", w.ID)
			return m.watchStore.UpdateStatus(ctx, w.ID, store.WatchStatusResolved)
		}
		return nil
	}

	value, err := m.metrics.Measure(ctx, metric, w.Service, w.Endpoint, w.Environment, window)
	if err != nil {
		return err
	}

	baselineValue := baselineValueForMetric(w.BaselineJSON, metric)
	if baselineValue == 0 {
		// Can't compare to zero baseline
		return nil
	}

	// If current value is within 10% of baseline, auto-resolve
	drift := math.Abs(value-baselineValue) / math.Abs(baselineValue)
	if drift <= 0.10 {
		slog.Info("auto-resolving watch", "watch_id", w.ID,
			"metric", metric, "value", value, "baseline", baselineValue, "drift_pct", drift*100)
		return m.watchStore.UpdateStatus(ctx, w.ID, store.WatchStatusResolved)
	}
	return nil
}

// Cleanup expires watches past their expiry and runs any housekeeping.
func (m *WatchSessionManager) Cleanup(ctx context.Context) {
	n, err := m.watchStore.ExpireWatches(ctx)
	if err != nil {
		slog.Error("watch cleanup: expire error", "error", err)
		return
	}
	if n > 0 {
		slog.Info("expired watches", "count", n)
	}
}

