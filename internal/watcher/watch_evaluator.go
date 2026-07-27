package watcher

import (
	"context"
	"fmt"
	"sync"
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

	// transitionMu serializes the active→triggered check-and-set per watch.
	// The periodic WatchScheduler and the reactive WatchStreamEvaluator (up to
	// 16 concurrent) share one *WatchEvaluator, so without this both can read a
	// stale "not triggered" snapshot and both create + notify. This is a single
	// process (SQLite, one node), so an in-process keyed lock plus a fresh
	// status re-read gives the same guarantee as a DB compare-and-swap.
	transitionMu keyedMutex
}

// NewWatchEvaluator creates a new WatchEvaluator.
func NewWatchEvaluator(metrics *WatchMetrics, watchStore store.WatchStore) *WatchEvaluator {
	return &WatchEvaluator{
		metrics:    metrics,
		watchStore: watchStore,
	}
}

// keyedMutex provides per-key mutual exclusion. The zero value is ready to use.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// lock acquires the mutex for key and returns its unlock function.
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = make(map[string]*sync.Mutex)
	}
	m, ok := k.locks[key]
	if !ok {
		m = &sync.Mutex{}
		k.locks[key] = m
	}
	k.mu.Unlock()

	m.Lock()
	return m.Unlock
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

	// Alert suppression fast-path: the snapshot already shows triggered.
	if w.Status == store.WatchStatusTriggered {
		result.Summary = fmt.Sprintf("%s (already triggered, suppressing)", summary)
		return result, nil
	}

	// Atomically flip active→triggered. Only the evaluation that actually wins
	// the transition fires the alert; a concurrent evaluation working from the
	// same stale snapshot loses and suppresses. This prevents duplicate/flapping
	// alerts when the scheduler and stream evaluator race on the same watch.
	won, err := e.transitionToTriggered(ctx, w.ID)
	if err != nil {
		return nil, err
	}
	if !won {
		result.Summary = fmt.Sprintf("%s (already triggered, suppressing)", summary)
		return result, nil
	}

	// Fire alert.
	result.HasAlert = true
	result.Summary = fmt.Sprintf("%s (%d consecutive breaches)", summary, breaches)

	return result, nil
}

// transitionToTriggered atomically moves a watch from any non-triggered status
// to "triggered". It returns true only for the caller that performed the
// transition, so exactly one concurrent evaluation fires the alert. The keyed
// lock serializes the read-check-write, and the authoritative status is
// re-read from the store (not the caller's snapshot) inside the lock.
func (e *WatchEvaluator) transitionToTriggered(ctx context.Context, id string) (bool, error) {
	unlock := e.transitionMu.lock(id)
	defer unlock()

	cur, err := e.watchStore.GetByID(ctx, id)
	if err != nil {
		return false, fmt.Errorf("reading watch status: %w", err)
	}
	if cur.Status == store.WatchStatusTriggered {
		return false, nil // another evaluation already won the transition
	}
	if err := e.watchStore.UpdateStatus(ctx, id, store.WatchStatusTriggered); err != nil {
		return false, fmt.Errorf("updating watch to triggered: %w", err)
	}
	return true, nil
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
