package watcher

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// WatchStreamEvaluator reactively checks watches when new logs arrive.
type WatchStreamEvaluator struct {
	watchStore      store.WatchStore
	evaluator       *WatchEvaluator
	evidenceBuilder *WatchEvidenceBuilder

	// Sliding window: per-watch counters to avoid over-evaluation
	mu       sync.Mutex
	lastEval map[string]time.Time // watch ID → last evaluation time
	minGap   time.Duration        // minimum gap between evaluations for the same watch

	// Semaphore to limit concurrent evaluations
	sem chan struct{}
}

// NewWatchStreamEvaluator creates a reactive stream evaluator.
func NewWatchStreamEvaluator(
	watchStore store.WatchStore,
	evaluator *WatchEvaluator,
	evidenceBuilder *WatchEvidenceBuilder,
) *WatchStreamEvaluator {
	return &WatchStreamEvaluator{
		watchStore:      watchStore,
		evaluator:       evaluator,
		evidenceBuilder: evidenceBuilder,
		lastEval:        make(map[string]time.Time),
		minGap:          10 * time.Second,
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

	// Collect unique services from entries
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
		// Semaphore full — skip evaluation to prevent goroutine explosion
	}
}

func (s *WatchStreamEvaluator) evaluateMatching(services map[string]bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	watches, err := s.watchStore.List(ctx, store.ListWatchParams{Status: store.WatchStatusActive})
	if err != nil {
		slog.Warn("watch stream: listing watches", "error", err)
		return
	}

	now := time.Now()
	for _, w := range watches {
		// Only evaluate watches matching incoming services
		if w.Service != "" && !services[w.Service] {
			continue
		}

		// Throttle: skip if evaluated recently
		s.mu.Lock()
		last, ok := s.lastEval[w.ID]
		if ok && now.Sub(last) < s.minGap {
			s.mu.Unlock()
			continue
		}
		s.lastEval[w.ID] = now
		s.mu.Unlock()

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
		_, err := s.watchStore.CreateAlert(ctx, store.CreateWatchAlertParams{
			WatchID:        w.ID,
			RunID:          run.ID,
			Urgency:        w.Urgency,
			Summary:        result.Summary,
			TriggerMetric:  string(w.Metric),
			TriggerValue:   result.Value,
			ThresholdValue: w.Threshold,
			Evidence:       evidence,
		})
		if err != nil {
			slog.Error("watch stream: creating alert", "watch_id", w.ID, "error", err)
		}
	}
}
