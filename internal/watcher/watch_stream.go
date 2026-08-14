package watcher

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/safe"
	"github.com/adham90/opentrace/pkg/store"
)

const (
	// maxConcurrentStreamEvals bounds reactive evaluations triggered by ingest.
	maxConcurrentStreamEvals = 16
	// streamEvalTimeout bounds one reactive evaluation pass.
	streamEvalTimeout = 30 * time.Second
	// defaultStreamCheckInterval is used when a watch's check_interval is bad.
	defaultStreamCheckInterval = 30 * time.Second
	// streamStopDrainTimeout bounds how long Stop waits for in-flight reactive
	// evaluations and their alert deliveries. It must stay comfortably inside
	// the process-wide shutdown budget so a wedged notifier delays exit rather
	// than blocking it, while a healthy delivery still gets to land.
	streamStopDrainTimeout = 5 * time.Second
)

// WatchStreamEvaluator reactively checks watches when new logs arrive.
// It coordinates with the WatchScheduler through the DB — both paths
// use last_checked_at as the single source of truth to prevent duplicate
// evaluations.
type WatchStreamEvaluator struct {
	watchStore      store.WatchStore
	evaluator       *WatchEvaluator
	evidenceBuilder *WatchEvidenceBuilder
	notifiers       []WatchAlertNotifier
	notify          *notifyDispatcher

	// Parent context for deriving per-evaluation timeouts.
	ctx context.Context

	// Semaphore to limit concurrent evaluations.
	sem chan struct{}

	// mu guards stopped and serialises evalWG.Add against Stop's Wait.
	mu sync.RWMutex
	// stopped rejects new reactive evaluations once shutdown has begun.
	stopped bool
	// evalWG tracks in-flight reactive evaluations so Stop can drain the
	// alert deliveries they are about to dispatch.
	evalWG sync.WaitGroup
}

// NewWatchStreamEvaluator creates a reactive stream evaluator.
func NewWatchStreamEvaluator(
	ctx context.Context,
	watchStore store.WatchStore,
	evaluator *WatchEvaluator,
	evidenceBuilder *WatchEvidenceBuilder,
	notifiers []WatchAlertNotifier,
) *WatchStreamEvaluator {
	if ctx == nil {
		ctx = context.Background()
	}
	return &WatchStreamEvaluator{
		ctx:             ctx,
		watchStore:      watchStore,
		evaluator:       evaluator,
		evidenceBuilder: evidenceBuilder,
		notifiers:       notifiers,
		notify:          newNotifyDispatcher(),
		sem:             make(chan struct{}, maxConcurrentStreamEvals),
	}
}

// OnLogsReceived is called after new logs are ingested. It checks if any
// active watch matches the incoming data and triggers async evaluation.
// This method is non-blocking.
func (s *WatchStreamEvaluator) OnLogsReceived(entries []store.LogEntry) {
	if len(entries) == 0 {
		return
	}

	// Collect unique services from entries.
	services := make(map[string]bool)
	for _, e := range entries {
		if e.Service != "" {
			services[e.Service] = true
		}
	}

	if !s.beginEval() {
		// Shutting down, or semaphore full — skip to prevent goroutine explosion.
		return
	}
	go func() {
		defer s.endEval()
		safe.Run("watcher.evaluateMatching", func() { s.evaluateMatching(services) })
	}()
}

// beginEval reserves a semaphore slot and registers the evaluation with the
// shutdown drain. It reports false when the evaluator is stopping or saturated.
// Taking the read lock here (and the write lock in Stop) guarantees no
// evalWG.Add races Stop's Wait.
func (s *WatchStreamEvaluator) beginEval() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.stopped {
		return false
	}
	select {
	case s.sem <- struct{}{}:
		s.evalWG.Add(1)
		return true
	default:
		return false
	}
}

func (s *WatchStreamEvaluator) endEval() {
	<-s.sem
	s.evalWG.Done()
}

// Stop shuts the reactive evaluator down: it refuses new evaluations, then
// drains the ones already running and the alert deliveries they dispatched.
// Without this, an alert that fired during the last ingest batch before exit
// would be created in the database but never delivered.
func (s *WatchStreamEvaluator) Stop() {
	s.mu.Lock()
	already := s.stopped
	s.stopped = true
	s.mu.Unlock()
	if already {
		return
	}

	deadline := time.Now().Add(streamStopDrainTimeout)
	if !waitGroupFor(&s.evalWG, streamStopDrainTimeout) {
		slog.Warn("watch stream: in-flight evaluations did not finish before shutdown deadline",
			"timeout", streamStopDrainTimeout)
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		slog.Warn("watch stream: alert deliveries abandoned at shutdown deadline",
			"timeout", streamStopDrainTimeout)
		return
	}
	if s.notify != nil && !s.notify.waitFor(remaining) {
		slog.Warn("watch stream: alert deliveries abandoned at shutdown deadline",
			"timeout", streamStopDrainTimeout)
	}
}

func (s *WatchStreamEvaluator) evaluateMatching(services map[string]bool) {
	ctx, cancel := context.WithTimeout(s.ctx, streamEvalTimeout)
	defer cancel()

	watches, err := s.watchStore.List(ctx, store.ListWatchParams{Status: store.WatchStatusActive})
	if err != nil {
		slog.Warn("watch stream: listing watches", "error", err)
		return
	}

	now := time.Now()

	for _, w := range watches {
		// Only evaluate watches matching incoming services.
		if w.Service != "" && !services[w.Service] {
			continue
		}

		// Coordination: skip if recently checked (by scheduler or a previous stream eval).
		// Both paths write last_checked_at via UpdateAfterCheck, so this is the single
		// source of truth — no in-memory state needed.
		if w.LastCheckedAt != nil {
			ci := parseDurationOr(w.CheckInterval, defaultStreamCheckInterval)
			// Use half the check interval as the minimum gap for reactive checks.
			// This gives the stream evaluator a chance to fire mid-cycle while
			// still preventing duplicate evaluations.
			if now.Sub(*w.LastCheckedAt) < ci/2 {
				continue
			}
		}

		s.evaluateOne(ctx, &w)
	}
}

func (s *WatchStreamEvaluator) evaluateOne(ctx context.Context, w *store.Watch) {
	run, err := s.watchStore.CreateRun(ctx, w.ID)
	if err != nil {
		slog.Warn("watch stream: creating run", "watch_id", w.ID, "error", err)
		return
	}

	result, err := s.evaluator.Evaluate(ctx, w)
	if err != nil {
		slog.Warn("watch stream: evaluation failed", "watch_id", w.ID, "error", err)
		_ = s.watchStore.FailRun(ctx, run.ID, err.Error())
		return
	}

	_ = s.watchStore.CompleteRun(ctx, run.ID, result.Value, result.Breached, result.Summary)

	if result.HasAlert {
		var evidence *store.WatchEvidenceBundle
		if s.evidenceBuilder != nil {
			ev, err := s.evidenceBuilder.Build(ctx, w, result)
			if err == nil {
				evidence = ev
			}
		}
		alert, err := s.watchStore.CreateAlert(ctx, store.CreateWatchAlertParams{
			WatchID:            w.ID,
			RunID:              run.ID,
			Urgency:            w.Urgency,
			Summary:            result.Summary,
			ConditionsSnapshot: store.BuildConditionsSnapshot(store.ConditionsSummary(w.ConditionsJSON), result.Value, store.ConditionsThreshold(w.ConditionsJSON)),
			Evidence:           evidence,
		})
		if err != nil {
			slog.Error("watch stream: creating alert", "watch_id", w.ID, "error", err)
			return
		}
		// Dispatch off this goroutine: it holds one of the semaphore slots
		// that bound reactive evaluation.
		s.notify.dispatch(ctx, s.notifiers, alert, w)
	}
}
