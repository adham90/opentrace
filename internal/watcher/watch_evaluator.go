package watcher

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
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

// Evaluate measures the watch's conditions and determines if an alert should fire.
// If the watch has a JSON conditions tree, it uses the tree evaluator.
// Otherwise, it falls back to the flat metric/operator/threshold fields.
func (e *WatchEvaluator) Evaluate(ctx context.Context, w *store.Watch) (*WatchEvalResult, error) {
	// Parse the check interval for the measurement window.
	window, err := time.ParseDuration(w.CheckInterval)
	if err != nil {
		window = 30 * time.Second
	}
	bw, err := time.ParseDuration(w.BaselineWindow)
	if err == nil && bw > window {
		window = bw
	}

	// Parse and evaluate the conditions tree.
	cond, err := ParseCondition(w.ConditionsJSON)
	if err != nil {
		return nil, fmt.Errorf("parsing conditions: %w", err)
	}
	condResult, err := EvaluateCondition(ctx, cond, e.metrics, w.BaselineJSON, w.Environment, window)
	if err != nil {
		return nil, fmt.Errorf("evaluating conditions: %w", err)
	}
	value := condResult.Value
	breached := condResult.Breached
	summary := condResult.Summary

	// Update consecutive breaches.
	breaches := 0
	if breached {
		breaches = w.ConsecutiveBreaches + 1
	}

	// Calculate next check time.
	ci, err := time.ParseDuration(w.CheckInterval)
	if err != nil {
		ci = 30 * time.Second
	}
	nextCheck := time.Now().UTC().Add(ci)

	// Persist the check result.
	if err := e.watchStore.UpdateAfterCheck(ctx, w.ID, value, breaches, nextCheck); err != nil {
		return nil, fmt.Errorf("updating after check: %w", err)
	}

	result := &WatchEvalResult{
		Value:    value,
		Breached: breached,
		Summary:  summary,
	}

	// Not breached → no alert.
	if !breached {
		return result, nil
	}

	// Check consecutive breach requirement.
	if breaches < w.MinConsecutive {
		result.Summary = fmt.Sprintf("%s (%d/%d consecutive)", summary, breaches, w.MinConsecutive)
		return result, nil
	}

	// Alert suppression: don't re-alert if already triggered.
	if w.Status == store.WatchStatusTriggered {
		result.Summary = fmt.Sprintf("%s (already triggered, suppressing)", summary)
		return result, nil
	}

	// Fire alert.
	result.HasAlert = true
	result.Summary = fmt.Sprintf("%s (%d consecutive breaches)", summary, breaches)

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
