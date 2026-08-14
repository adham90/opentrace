package watcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// defaultCheckInterval is used when a watch's check_interval is missing or
// malformed.
const defaultCheckInterval = 30 * time.Second

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

	// transitionMu serializes each watch's whole evaluation: the breach-counter
	// read-modify-write and the active→triggered check-and-set. The periodic
	// WatchScheduler and the reactive WatchStreamEvaluator (up to 16
	// concurrent) share one *WatchEvaluator, so without this both can read a
	// stale snapshot and both increment from it (losing one increment) and
	// both create + notify. This is a single process (SQLite, one node), so an
	// in-process keyed lock plus a fresh re-read gives the same guarantee as a
	// DB compare-and-swap.
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
// Entries are reference-counted and removed once the last holder releases them,
// so a long-lived evaluator does not accumulate one map entry per ephemeral
// watch for the process lifetime.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*refCountedMutex
}

type refCountedMutex struct {
	mu   sync.Mutex
	refs int
}

// lock acquires the mutex for key and returns its unlock function.
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = make(map[string]*refCountedMutex)
	}
	m, ok := k.locks[key]
	if !ok {
		m = &refCountedMutex{}
		k.locks[key] = m
	}
	m.refs++
	k.mu.Unlock()

	m.mu.Lock()
	return func() {
		m.mu.Unlock()
		k.mu.Lock()
		m.refs--
		if m.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}

// size reports how many per-key mutexes are currently tracked (test hook).
func (k *keyedMutex) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.locks)
}

// Evaluate measures the watch's conditions and determines if an alert should fire.
//
// The whole evaluation runs under the watch's keyed lock and re-reads the
// authoritative row from the store, so the consecutive-breach counter is a
// read-modify-write over fresh state rather than over the caller's snapshot.
// The scheduler tick and the reactive stream evaluator share one
// *WatchEvaluator in a single process, so serializing here is what keeps two
// concurrent observations of the same breach from collapsing into one
// increment (UpdateAfterCheck is a plain overwrite, not a SQL increment).
func (e *WatchEvaluator) Evaluate(ctx context.Context, w *store.Watch) (*WatchEvalResult, error) {
	unlock := e.transitionMu.lock(w.ID)
	defer unlock()

	// Re-read the authoritative state; the caller's snapshot may predate a
	// concurrent evaluation's write.
	cur, err := e.watchStore.GetByID(ctx, w.ID)
	if err != nil {
		return nil, fmt.Errorf("reading watch: %w", err)
	}

	result, err := e.evaluateLocked(ctx, cur)
	if err != nil {
		e.recordFailedCheck(ctx, cur, err)
		return nil, err
	}
	return result, nil
}

// evaluateLocked performs the measurement and state transition for a watch.
// The caller must hold the watch's keyed lock and pass a freshly read row.
func (e *WatchEvaluator) evaluateLocked(ctx context.Context, w *store.Watch) (*WatchEvalResult, error) {
	// The measurement window is the check interval. It is deliberately NOT
	// widened to baseline_window: doing so measured a "logs per 30s check"
	// threshold over a trailing hour (120x inflation at the defaults) and made
	// min_consecutive meaningless because successive checks re-measured the
	// same data. Relative conditions measure over the baseline's own window
	// (see evalRelative), which is where the baseline comparison needs it.
	window := parseDurationOr(w.CheckInterval, defaultCheckInterval)

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

	// Update consecutive breaches from the authoritative counter.
	breaches := 0
	if breached {
		breaches = w.ConsecutiveBreaches + 1
	}

	nextCheck := time.Now().UTC().Add(parseDurationOr(w.CheckInterval, defaultCheckInterval))

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

	// Alert suppression: the authoritative row already shows triggered, so a
	// concurrent evaluation won the transition first.
	if w.Status == store.WatchStatusTriggered {
		result.Summary = fmt.Sprintf("%s (already triggered, suppressing)", summary)
		return result, nil
	}

	if err := e.watchStore.UpdateStatus(ctx, w.ID, store.WatchStatusTriggered); err != nil {
		return nil, fmt.Errorf("updating watch to triggered: %w", err)
	}

	// Fire alert.
	result.HasAlert = true
	result.Summary = fmt.Sprintf("%s (%d consecutive breaches)", summary, breaches)

	return result, nil
}

// recordFailedCheck advances the watch's schedule after a failed evaluation.
//
// Without this, next_check_at and last_checked_at never move: GetDueWatches
// returns the watch on every 15s tick, and the stream evaluator's
// last_checked_at gate never engages, so one malformed watch is re-evaluated
// on every ingest batch forever. A permanently invalid definition is disabled
// outright rather than retried — it can never succeed.
func (e *WatchEvaluator) recordFailedCheck(ctx context.Context, w *store.Watch, evalErr error) {
	if errors.Is(evalErr, ErrInvalidWatchConfig) {
		slog.Warn("disabling watch with an invalid definition",
			"watch_id", w.ID,
			"conditions", store.ConditionsSummary(w.ConditionsJSON),
			"error", evalErr,
		)
		// No dedicated "errored" status exists; expired is the terminal state
		// that stops both the scheduler and the stream evaluator from picking
		// the watch up again.
		if err := e.watchStore.UpdateStatus(ctx, w.ID, store.WatchStatusExpired); err != nil {
			slog.Error("disabling invalid watch failed", "watch_id", w.ID, "error", err)
		}
		return
	}

	// Transient failure: keep the counter, but move the schedule forward so the
	// failure is retried on the normal cadence instead of every tick.
	value := 0.0
	if w.CurrentValue != nil {
		value = *w.CurrentValue
	}
	nextCheck := time.Now().UTC().Add(parseDurationOr(w.CheckInterval, defaultCheckInterval))
	if err := e.watchStore.UpdateAfterCheck(ctx, w.ID, value, w.ConsecutiveBreaches, nextCheck); err != nil {
		slog.Error("rescheduling failed watch check", "watch_id", w.ID, "error", err)
	}
}

// compare applies a watch operator. An unrecognized operator is a
// configuration error, not a non-breach: silently returning false produced
// watches that ran green forever and could never fire.
func compare(value float64, op store.WatchOperator, threshold float64) (bool, error) {
	switch op {
	case store.WatchOpGreaterThan:
		return value > threshold, nil
	case store.WatchOpGreaterThanEqual:
		return value >= threshold, nil
	case store.WatchOpLessThan:
		return value < threshold, nil
	case store.WatchOpLessThanEqual:
		return value <= threshold, nil
	case store.WatchOpEqual:
		return value == threshold, nil
	case store.WatchOpNotEqual:
		return value != threshold, nil
	default:
		return false, configErrorf("unknown comparison operator %q (want gt, gte, lt, lte, eq or neq)", op)
	}
}
