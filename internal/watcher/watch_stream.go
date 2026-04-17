package watcher

import (
	"context"
	"log/slog"
	"time"

	"github.com/adham90/opentrace/pkg/store"
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

	// Parent context for deriving per-evaluation timeouts.
	ctx context.Context

	// Semaphore to limit concurrent evaluations.
	sem chan struct{}
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
		sem:             make(chan struct{}, 16),
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

	select {
	case s.sem <- struct{}{}:
		go func() {
			defer func() { <-s.sem }()
			s.evaluateMatching(services)
		}()
	default:
		// Semaphore full — skip to prevent goroutine explosion.
	}
}

func (s *WatchStreamEvaluator) evaluateMatching(services map[string]bool) {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
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
			ci, err := time.ParseDuration(w.CheckInterval)
			if err != nil {
				ci = 30 * time.Second
			}
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
			WatchID:        w.ID,
			RunID:          run.ID,
			Urgency:        w.Urgency,
			Summary:        result.Summary,
			ConditionsSnapshot: store.BuildConditionsSnapshot(store.ConditionsSummary(w.ConditionsJSON), result.Value, store.ConditionsThreshold(w.ConditionsJSON)),
			Evidence:       evidence,
		})
		if err != nil {
			slog.Error("watch stream: creating alert", "watch_id", w.ID, "error", err)
			return
		}
		if len(s.notifiers) > 0 {
			NotifyAllWatchAlert(ctx, s.notifiers, alert, w)
		}
	}
}
