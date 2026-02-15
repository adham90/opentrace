package watcher

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// WatchEvalResult holds the result of evaluating a single watch.
type WatchEvalResult struct {
	Value    float64
	Breached bool
	HasAlert bool
	Summary  string
}

// WatchEvaluator evaluates watches against their metric thresholds.
type WatchEvaluator struct {
	metrics    *WatchMetrics
	watchStore store.WatchStore
}

// NewWatchEvaluator creates a new WatchEvaluator.
func NewWatchEvaluator(metrics *WatchMetrics, watchStore store.WatchStore) *WatchEvaluator {
	return &WatchEvaluator{
		metrics:    metrics,
		watchStore: watchStore,
	}
}

// Evaluate measures the watch's metric and determines if an alert should fire.
func (e *WatchEvaluator) Evaluate(ctx context.Context, w *store.Watch) (*WatchEvalResult, error) {
	// Parse the check interval for the measurement window
	window, err := time.ParseDuration(w.CheckInterval)
	if err != nil {
		window = 30 * time.Second
	}
	// Use baseline window for measurement if larger
	bw, err := time.ParseDuration(w.BaselineWindow)
	if err == nil && bw > window {
		window = bw
	}

	value, err := e.metrics.Measure(ctx, w.Metric, w.Service, w.Endpoint, window)
	if err != nil {
		return nil, fmt.Errorf("measuring %s: %w", w.Metric, err)
	}

	breached := compare(value, w.Operator, w.Threshold)

	// Update consecutive breaches
	breaches := 0
	if breached {
		breaches = w.ConsecutiveBreaches + 1
	}

	// Calculate next check time
	ci, err := time.ParseDuration(w.CheckInterval)
	if err != nil {
		ci = 30 * time.Second
	}
	nextCheck := time.Now().UTC().Add(ci)

	// Persist the check result
	if err := e.watchStore.UpdateAfterCheck(ctx, w.ID, value, breaches, nextCheck); err != nil {
		return nil, fmt.Errorf("updating after check: %w", err)
	}

	result := &WatchEvalResult{
		Value:    value,
		Breached: breached,
	}

	// Determine if alert should fire
	if !breached {
		result.Summary = fmt.Sprintf("%s = %.4f (within threshold %.4f)", w.Metric, value, w.Threshold)
		return result, nil
	}

	// Check consecutive breach requirement
	if breaches < w.MinConsecutive {
		result.Summary = fmt.Sprintf("%s = %.4f breached (%d/%d consecutive)", w.Metric, value, breaches, w.MinConsecutive)
		return result, nil
	}

	// Alert suppression: don't re-alert if already triggered
	if w.Status == store.WatchStatusTriggered {
		result.Summary = fmt.Sprintf("%s = %.4f (already triggered, suppressing duplicate alert)", w.Metric, value)
		return result, nil
	}

	// Fire alert
	result.HasAlert = true
	result.Summary = fmt.Sprintf("%s = %.4f exceeds threshold %.4f (%d consecutive breaches)", w.Metric, value, w.Threshold, breaches)

	// Transition watch to triggered state
	if err := e.watchStore.UpdateStatus(ctx, w.ID, store.WatchStatusTriggered); err != nil {
		return nil, fmt.Errorf("updating watch to triggered: %w", err)
	}

	return result, nil
}

func compare(value float64, op store.WatchOperator, threshold float64) bool {
	switch op {
	case store.WatchOpGreaterThan:
		return value > threshold
	case store.WatchOpGreaterThanEqual:
		return value >= threshold
	case store.WatchOpLessThan:
		return value < threshold
	case store.WatchOpLessThanEqual:
		return value <= threshold
	case store.WatchOpEqual:
		return value == threshold
	case store.WatchOpNotEqual:
		return value != threshold
	default:
		return false
	}
}
