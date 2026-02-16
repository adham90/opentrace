package healthcheck

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// Scheduler polls enabled health checks and runs probes at their configured intervals.
type Scheduler struct {
	store   store.HealthCheckStore
	checker *Checker
	poll    time.Duration
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// mu protects lastRun map
	mu      sync.Mutex
	lastRun map[string]time.Time
}

// NewScheduler creates a health check scheduler.
// pollInterval controls how often we scan for due checks (default 15s).
func NewScheduler(hcStore store.HealthCheckStore, pollInterval time.Duration) *Scheduler {
	if pollInterval <= 0 {
		pollInterval = 15 * time.Second
	}
	return &Scheduler{
		store:   hcStore,
		checker: NewChecker(),
		poll:    pollInterval,
		lastRun: make(map[string]time.Time),
	}
}

// Start begins the background polling loop. Returns immediately.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(ctx)
	}()
	slog.Info("healthcheck scheduler started", "poll_interval", s.poll)
}

// Stop signals the scheduler to stop and waits for it to finish.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

func (s *Scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()

	// Run once immediately on startup
	s.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	checks, err := s.store.List(ctx)
	if err != nil {
		slog.Warn("healthcheck scheduler: failed to list checks", "error", err)
		return
	}

	now := time.Now()
	for _, hc := range checks {
		if !hc.Enabled {
			continue
		}
		if !s.isDue(hc, now) {
			continue
		}

		s.mu.Lock()
		s.lastRun[hc.ID] = now
		s.mu.Unlock()

		// Run check in a goroutine to avoid blocking the tick
		go s.runCheck(ctx, hc)
	}
}

func (s *Scheduler) isDue(hc store.HealthCheck, now time.Time) bool {
	s.mu.Lock()
	last, ok := s.lastRun[hc.ID]
	s.mu.Unlock()

	if !ok {
		return true // never run
	}
	interval := time.Duration(hc.IntervalSecs) * time.Second
	return now.Sub(last) >= interval
}

func (s *Scheduler) runCheck(ctx context.Context, hc store.HealthCheck) {
	result := s.checker.Check(ctx, hc)

	if err := s.store.RecordResult(ctx, result); err != nil {
		slog.Warn("healthcheck: failed to record result",
			"healthcheck_id", hc.ID,
			"name", hc.Name,
			"error", err,
		)
		return
	}

	if result.Status != store.HealthCheckUp {
		slog.Warn("healthcheck probe failed",
			"healthcheck_id", hc.ID,
			"name", hc.Name,
			"url", hc.URL,
			"status", string(result.Status),
			"error", result.Error,
		)
	}
}
