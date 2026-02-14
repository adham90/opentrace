package watcher

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/llm"
	"github.com/adham90/opentrace/internal/store"
)

// SchedulerOpts configures the watcher scheduler.
type SchedulerOpts struct {
	WatcherStore  store.WatcherStore
	RunStore      store.WatcherRunStore
	AlertStore    store.AlertStore
	Registry      *connector.Registry
	ProviderCache *llm.ProviderCache
	AgentCfg      agent.RunConfig
	EventHub      *EventHub
	PollInterval  time.Duration // how often to check for due watchers (default: 30s)
	MaxConcurrent int           // max concurrent watcher runs (default: 3)

	// Evaluators for each watcher type
	RuleEvaluator      *RuleEvaluator
	DeadmanEvaluator   Evaluator
	DiffEvaluator      Evaluator
	CompositeEvaluator Evaluator
	TrendEvaluator     Evaluator
	SequenceEvaluator  Evaluator
}

// Scheduler polls for due watchers and executes them in the background.
type Scheduler struct {
	executor     *Executor
	watcherStore store.WatcherStore
	poll         time.Duration
	maxConc      int
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	sem          chan struct{} // concurrency limiter, reused across polls
}

// NewScheduler creates a new watcher scheduler.
func NewScheduler(opts SchedulerOpts) *Scheduler {
	poll := opts.PollInterval
	if poll <= 0 {
		poll = 30 * time.Second
	}
	maxConc := opts.MaxConcurrent
	if maxConc <= 0 {
		maxConc = 3
	}

	executor := NewExecutor(
		opts.WatcherStore,
		opts.RunStore,
		opts.AlertStore,
		opts.Registry,
		opts.ProviderCache,
		opts.AgentCfg,
		opts.EventHub,
	)

	// Wire evaluators so scheduled watchers of all types work correctly.
	if opts.RuleEvaluator != nil {
		executor.SetRuleEvaluator(opts.RuleEvaluator)
	}
	if opts.DeadmanEvaluator != nil {
		executor.SetDeadmanEvaluator(opts.DeadmanEvaluator)
	}
	if opts.DiffEvaluator != nil {
		executor.SetDiffEvaluator(opts.DiffEvaluator)
	}
	if opts.CompositeEvaluator != nil {
		executor.SetCompositeEvaluator(opts.CompositeEvaluator)
	}
	if opts.TrendEvaluator != nil {
		executor.SetTrendEvaluator(opts.TrendEvaluator)
	}
	if opts.SequenceEvaluator != nil {
		executor.SetSequenceEvaluator(opts.SequenceEvaluator)
	}

	return &Scheduler{
		executor:     executor,
		watcherStore: opts.WatcherStore,
		poll:         poll,
		maxConc:      maxConc,
		sem:          make(chan struct{}, maxConc),
	}
}

// Start begins the background polling loop. It returns immediately.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)

	go func() {
		defer s.wg.Done()
		slog.Info("scheduler started", "poll_interval", s.poll, "max_concurrent", s.maxConc)

		ticker := time.NewTicker(s.poll)
		defer ticker.Stop()

		// Run once immediately on start
		s.pollAndExecute(ctx)

		for {
			select {
			case <-ctx.Done():
				slog.Info("scheduler stopped")
				return
			case <-ticker.C:
				s.pollAndExecute(ctx)
			}
		}
	}()
}

// Stop gracefully shuts down the scheduler and waits for in-flight runs.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

func (s *Scheduler) pollAndExecute(ctx context.Context) {
	// Expire any watchers past their expires_at before fetching due watchers.
	if n, err := s.watcherStore.ExpireWatchers(ctx); err != nil {
		slog.Error("expire watchers error", "error", err)
	} else if n > 0 {
		slog.Info("expired watchers", "count", n)
	}

	watchers, err := s.watcherStore.GetDueWatchers(ctx)
	if err != nil {
		slog.Error("poll error", "error", err)
		return
	}

	if len(watchers) == 0 {
		return
	}

	slog.Info("found due watchers", "count", len(watchers))

	for _, w := range watchers {
		if ctx.Err() != nil {
			return
		}

		s.sem <- struct{}{} // acquire
		s.wg.Add(1)

		go func(w store.Watcher) {
			defer s.wg.Done()
			defer func() { <-s.sem }() // release
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic executing watcher", "watcher_id", w.ID, "watcher_title", w.Title, "panic", r)
				}
			}()

			slog.Info("executing watcher", "watcher_id", w.ID, "watcher_title", w.Title)
			s.executor.Execute(ctx, w)
			slog.Info("finished watcher", "watcher_id", w.ID, "watcher_title", w.Title)
		}(w)
	}
}
