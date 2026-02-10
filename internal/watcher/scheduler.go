package watcher

import (
	"context"
	"log"
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
}

// Scheduler polls for due watchers and executes them in the background.
type Scheduler struct {
	executor     *Executor
	watcherStore store.WatcherStore
	poll         time.Duration
	maxConc      int
	cancel       context.CancelFunc
	wg           sync.WaitGroup
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

	return &Scheduler{
		executor:     executor,
		watcherStore: opts.WatcherStore,
		poll:         poll,
		maxConc:      maxConc,
	}
}

// Start begins the background polling loop. It returns immediately.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)

	go func() {
		defer s.wg.Done()
		log.Printf("watcher: scheduler started (poll=%s, max_concurrent=%d)", s.poll, s.maxConc)

		ticker := time.NewTicker(s.poll)
		defer ticker.Stop()

		// Run once immediately on start
		s.pollAndExecute(ctx)

		for {
			select {
			case <-ctx.Done():
				log.Println("watcher: scheduler stopped")
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
	watchers, err := s.watcherStore.GetDueWatchers(ctx)
	if err != nil {
		log.Printf("watcher: poll error: %v", err)
		return
	}

	if len(watchers) == 0 {
		return
	}

	log.Printf("watcher: found %d due watcher(s)", len(watchers))

	// Use a semaphore to limit concurrency
	sem := make(chan struct{}, s.maxConc)

	for _, w := range watchers {
		if ctx.Err() != nil {
			return
		}

		sem <- struct{}{} // acquire
		s.wg.Add(1)

		go func(w store.Watcher) {
			defer s.wg.Done()
			defer func() { <-sem }() // release

			log.Printf("watcher: executing %q (%s)", w.Title, w.ID)
			s.executor.Execute(ctx, w)
			log.Printf("watcher: finished %q (%s)", w.Title, w.ID)
		}(w)
	}
}
