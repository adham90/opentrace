package watcher

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/metrics"
	"github.com/adham90/opentrace/pkg/store"
)

// WatchSchedulerOpts configures the watch scheduler.
type WatchSchedulerOpts struct {
	WatchStore      store.WatchStore
	LogStore        store.LogStore
	Evaluator       *WatchEvaluator
	EvidenceBuilder *WatchEvidenceBuilder
	SessionManager  *WatchSessionManager
	Notifiers       []WatchAlertNotifier
	PollInterval    time.Duration
}

// WatchScheduler polls for due watches and evaluates them.
type WatchScheduler struct {
	watchStore      store.WatchStore
	evaluator       *WatchEvaluator
	evidenceBuilder *WatchEvidenceBuilder
	sessionManager  *WatchSessionManager
	notifiers       []WatchAlertNotifier
	poll            time.Duration
	cancel          context.CancelFunc
	wg              sync.WaitGroup
}

// NewWatchScheduler creates a new watch scheduler.
func NewWatchScheduler(opts WatchSchedulerOpts) *WatchScheduler {
	poll := opts.PollInterval
	if poll <= 0 {
		poll = 15 * time.Second
	}
	notifiers := opts.Notifiers
	if len(notifiers) == 0 {
		notifiers = []WatchAlertNotifier{&WatchLogNotifier{}}
	}
	return &WatchScheduler{
		watchStore:      opts.WatchStore,
		evaluator:       opts.Evaluator,
		evidenceBuilder: opts.EvidenceBuilder,
		sessionManager:  opts.SessionManager,
		notifiers:       notifiers,
		poll:            poll,
	}
}

// Start begins the background polling loop. Returns immediately.
func (s *WatchScheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)

	go func() {
		defer s.wg.Done()
		slog.Info("watch scheduler started", "poll_interval", s.poll)

		ticker := time.NewTicker(s.poll)
		defer ticker.Stop()

		s.tick(ctx)

		for {
			select {
			case <-ctx.Done():
				slog.Info("watch scheduler stopped")
				return
			case <-ticker.C:
				s.tick(ctx)
			}
		}
	}()
}

// Stop gracefully shuts down the scheduler.
func (s *WatchScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

func (s *WatchScheduler) tick(ctx context.Context) {
	// Cleanup expired watches
	if s.sessionManager != nil {
		s.sessionManager.Cleanup(ctx)
	}

	// Check for auto-resolve on triggered watches
	s.checkTriggeredWatches(ctx)

	// Evaluate due watches
	watches, err := s.watchStore.GetDueWatches(ctx)
	if err != nil {
		slog.Error("watch scheduler poll error", "error", err)
		return
	}

	for _, w := range watches {
		if ctx.Err() != nil {
			return
		}
		s.evaluateWatch(ctx, &w)
	}
}

func (s *WatchScheduler) checkTriggeredWatches(ctx context.Context) {
	if s.sessionManager == nil {
		return
	}
	watches, err := s.watchStore.List(ctx, store.ListWatchParams{Status: store.WatchStatusTriggered})
	if err != nil {
		slog.Error("watch scheduler: listing triggered watches", "error", err)
		return
	}
	for _, w := range watches {
		if ctx.Err() != nil {
			return
		}
		if err := s.sessionManager.CheckAutoResolve(ctx, &w); err != nil {
			slog.Warn("watch scheduler: auto-resolve check failed", "watch_id", w.ID, "error", err)
		}
	}
}

func (s *WatchScheduler) evaluateWatch(ctx context.Context, w *store.Watch) {
	// Create a run record
	run, err := s.watchStore.CreateRun(ctx, w.ID)
	if err != nil {
		slog.Error("watch scheduler: creating run", "watch_id", w.ID, "error", err)
		return
	}

	result, err := s.evaluator.Evaluate(ctx, w)
	if err != nil {
		slog.Warn("watch evaluation failed",
			"watch_id", w.ID,
			"conditions", store.ConditionsSummary(w.ConditionsJSON),
			"service", w.Service,
			"error", err,
		)
		_ = s.watchStore.FailRun(ctx, run.ID, err.Error())
		metrics.RecordWatcherEvaluation("error")
		return
	}

	// Complete the run
	if err := s.watchStore.CompleteRun(ctx, run.ID, result.Value, result.Breached, result.Summary); err != nil {
		slog.Error("watch scheduler: completing run", "watch_id", w.ID, "error", err)
	}

	// Record Prometheus metrics for watcher evaluation outcome
	if result.HasAlert {
		metrics.RecordWatcherEvaluation("alert")
	} else {
		metrics.RecordWatcherEvaluation("ok")
	}

	// If alert should fire, build evidence and create alert
	if result.HasAlert {
		s.createAlert(ctx, w, run, result)
	}
}

func (s *WatchScheduler) createAlert(ctx context.Context, w *store.Watch, run *store.WatchRun, result *WatchEvalResult) {
	var evidence *store.WatchEvidenceBundle
	if s.evidenceBuilder != nil {
		ev, err := s.evidenceBuilder.Build(ctx, w, result)
		if err != nil {
			slog.Warn("watch evidence build failed", "watch_id", w.ID, "error", err)
		} else {
			evidence = ev
		}
	}

	alert, err := s.watchStore.CreateAlert(ctx, store.CreateWatchAlertParams{
		WatchID:        w.ID,
		RunID:          run.ID,
		Urgency:        w.Urgency,
		Summary:        result.Summary,
		TriggerMetric:  store.ConditionsSummary(w.ConditionsJSON),
		TriggerValue:   result.Value,
		ThresholdValue: store.ConditionsThreshold(w.ConditionsJSON),
		Evidence:       evidence,
	})
	if err != nil {
		slog.Error("watch scheduler: creating alert", "watch_id", w.ID, "error", err)
		return
	}

	slog.Info("watch alert created", "watch_id", w.ID, "conditions", store.ConditionsSummary(w.ConditionsJSON), "value", result.Value)

	// Dispatch notifications asynchronously
	if len(s.notifiers) > 0 {
		NotifyAllWatchAlert(ctx, s.notifiers, alert, w)
	}
}
