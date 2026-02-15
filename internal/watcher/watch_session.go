package watcher

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/adham90/opentrace/internal/store"
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

	value, err := m.metrics.Measure(ctx, w.Metric, w.Service, w.Endpoint, window)
	if err != nil {
		return err
	}

	baselineValue := getBaselineMetricValue(w)
	if baselineValue == 0 {
		// Can't compare to zero baseline
		return nil
	}

	// If current value is within 10% of baseline, auto-resolve
	drift := math.Abs(value-baselineValue) / math.Abs(baselineValue)
	if drift <= 0.10 {
		slog.Info("auto-resolving watch", "watch_id", w.ID, "metric", w.Metric,
			"value", value, "baseline", baselineValue, "drift_pct", drift*100)
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

func getBaselineMetricValue(w *store.Watch) float64 {
	if w.BaselineJSON == nil {
		return 0
	}
	switch w.Metric {
	case store.WatchMetricErrorRate:
		return w.BaselineJSON.ErrorRate
	case store.WatchMetricResponseTime:
		return w.BaselineJSON.AvgResponseMs
	case store.WatchMetricP95Response:
		return w.BaselineJSON.P95ResponseMs
	case store.WatchMetricLogCount:
		return float64(w.BaselineJSON.LogCount)
	case store.WatchMetricErrorCount:
		return float64(w.BaselineJSON.ErrorCount)
	case store.WatchMetricSQLCount:
		return w.BaselineJSON.SQLCount
	case store.WatchMetricCacheHitRate:
		return w.BaselineJSON.CacheHitRate
	default:
		return 0
	}
}
