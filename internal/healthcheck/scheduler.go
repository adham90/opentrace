package healthcheck

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/metrics"
	"github.com/adham90/opentrace/internal/store"
)

// recentResultsWindow is the number of recent results kept per check for reliability calculation.
const recentResultsWindow = 5

// maxBackoffMultiplier caps the backoff at 4x the configured interval.
const maxBackoffMultiplier = 4

// backoffThreshold is the number of consecutive failures before backoff kicks in.
const backoffThreshold = 3

// backoffState tracks exponential backoff for a failing health check.
type backoffState struct {
	consecutiveFailures int
}

// resultRing is a circular buffer of recent health check statuses.
type resultRing struct {
	results [recentResultsWindow]store.HealthCheckStatus
	count   int
	pos     int
}

// add appends a status to the ring buffer.
func (r *resultRing) add(status store.HealthCheckStatus) {
	r.results[r.pos%recentResultsWindow] = status
	r.pos++
	if r.count < recentResultsWindow {
		r.count++
	}
}

// recentResults returns the stored results (oldest first).
func (r *resultRing) recentResults() []store.HealthCheckStatus {
	if r.count == 0 {
		return nil
	}
	out := make([]store.HealthCheckStatus, r.count)
	start := 0
	if r.count == recentResultsWindow {
		start = r.pos % recentResultsWindow
	}
	for i := 0; i < r.count; i++ {
		out[i] = r.results[(start+i)%recentResultsWindow]
	}
	return out
}

// reliability returns the percentage of "up" results (0-100).
func (r *resultRing) reliability() float64 {
	if r.count == 0 {
		return 0
	}
	upCount := 0
	results := r.recentResults()
	for _, s := range results {
		if s == store.HealthCheckUp {
			upCount++
		}
	}
	return float64(upCount) / float64(r.count) * 100.0
}

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

	// sem limits concurrent health check goroutines
	sem chan struct{}

	// notifiers receive alerts on status transitions
	notifiers []HealthCheckAlertNotifier

	// statusMu protects lastStatus map for transition detection
	statusMu   sync.Mutex
	lastStatus map[string]store.HealthCheckStatus

	// backoffMu protects the backoff state map
	backoffMu sync.Mutex
	backoff   map[string]*backoffState

	// recentMu protects the recent results ring buffers
	recentMu sync.Mutex
	recent   map[string]*resultRing
}

// NewScheduler creates a health check scheduler.
// pollInterval controls how often we scan for due checks (default 15s).
// notifiers receive alerts on status transitions; if none are provided a log notifier is used.
func NewScheduler(hcStore store.HealthCheckStore, pollInterval time.Duration, notifiers ...HealthCheckAlertNotifier) *Scheduler {
	if pollInterval <= 0 {
		pollInterval = 15 * time.Second
	}
	if len(notifiers) == 0 {
		notifiers = []HealthCheckAlertNotifier{&HealthCheckLogNotifier{}}
	}
	return &Scheduler{
		store:      hcStore,
		checker:    NewChecker(),
		poll:       pollInterval,
		lastRun:    make(map[string]time.Time),
		sem:        make(chan struct{}, 16),
		notifiers:  notifiers,
		lastStatus: make(map[string]store.HealthCheckStatus),
		backoff:    make(map[string]*backoffState),
		recent:     make(map[string]*resultRing),
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

// RecentResults returns the last N statuses for the given check ID.
func (s *Scheduler) RecentResults(checkID string) []store.HealthCheckStatus {
	s.recentMu.Lock()
	defer s.recentMu.Unlock()
	ring, ok := s.recent[checkID]
	if !ok {
		return nil
	}
	return ring.recentResults()
}

// Reliability returns the recent reliability percentage (0-100) for the given check ID.
func (s *Scheduler) Reliability(checkID string) float64 {
	s.recentMu.Lock()
	defer s.recentMu.Unlock()
	ring, ok := s.recent[checkID]
	if !ok {
		return 0
	}
	return ring.reliability()
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
	checks, err := s.store.List(ctx, store.ListHealthCheckParams{})
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

		// Run check in a goroutine with bounded concurrency
		select {
		case s.sem <- struct{}{}:
			go func(hc store.HealthCheck) {
				defer func() { <-s.sem }()
				s.runCheck(ctx, hc)
			}(hc)
		default:
			slog.Debug("healthcheck: concurrency limit reached, skipping",
				"healthcheck_id", hc.ID, "name", hc.Name)
		}
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

	// Apply exponential backoff for checks that have consecutive failures.
	s.backoffMu.Lock()
	bs, hasBackoff := s.backoff[hc.ID]
	s.backoffMu.Unlock()
	if hasBackoff && bs.consecutiveFailures >= backoffThreshold {
		// Double the interval for every failure past the threshold, up to 4x.
		multiplier := 1 << (bs.consecutiveFailures - backoffThreshold + 1) // 2, 4, 8, ...
		if multiplier > maxBackoffMultiplier {
			multiplier = maxBackoffMultiplier
		}
		interval = time.Duration(multiplier) * interval
	}

	return now.Sub(last) >= interval
}

// EffectiveInterval returns the current interval for a check, accounting for backoff.
// Exported for testing.
func (s *Scheduler) EffectiveInterval(hc store.HealthCheck) time.Duration {
	interval := time.Duration(hc.IntervalSecs) * time.Second

	s.backoffMu.Lock()
	bs, hasBackoff := s.backoff[hc.ID]
	s.backoffMu.Unlock()

	if hasBackoff && bs.consecutiveFailures >= backoffThreshold {
		multiplier := 1 << (bs.consecutiveFailures - backoffThreshold + 1)
		if multiplier > maxBackoffMultiplier {
			multiplier = maxBackoffMultiplier
		}
		interval = time.Duration(multiplier) * interval
	}

	return interval
}

func (s *Scheduler) runCheck(ctx context.Context, hc store.HealthCheck) {
	result := s.checker.Check(ctx, hc)

	if err := s.store.RecordResult(ctx, result); err != nil {
		slog.Warn("healthcheck: failed to record result",
			"healthcheck_id", hc.ID,
			"name", hc.Name,
			"error", err,
		)
		metrics.RecordHealthCheckRun("error")
		return
	}

	// Record Prometheus metrics for health check result
	metrics.RecordHealthCheckRun(string(result.Status))

	if result.Status != store.HealthCheckUp {
		slog.Warn("healthcheck probe failed",
			"healthcheck_id", hc.ID,
			"name", hc.Name,
			"url", hc.URL,
			"status", string(result.Status),
			"error", result.Error,
		)
	}

	// Update backoff state.
	s.backoffMu.Lock()
	bs, ok := s.backoff[hc.ID]
	if !ok {
		bs = &backoffState{}
		s.backoff[hc.ID] = bs
	}
	if result.Status == store.HealthCheckUp {
		bs.consecutiveFailures = 0
	} else {
		bs.consecutiveFailures++
	}
	s.backoffMu.Unlock()

	// Update rolling window of recent results.
	s.recentMu.Lock()
	ring, ok := s.recent[hc.ID]
	if !ok {
		ring = &resultRing{}
		s.recent[hc.ID] = ring
	}
	ring.add(result.Status)
	s.recentMu.Unlock()

	// Check for status transition and fire notifications
	s.statusMu.Lock()
	prev, known := s.lastStatus[hc.ID]
	s.lastStatus[hc.ID] = result.Status
	s.statusMu.Unlock()

	if known && prev != result.Status {
		alert := &HealthCheckAlert{
			HealthCheckID:   hc.ID,
			HealthCheckName: hc.Name,
			URL:             hc.URL,
			PreviousStatus:  prev,
			CurrentStatus:   result.Status,
			StatusCode:      result.StatusCode,
			ResponseMs:      result.ResponseMs,
			ErrorMessage:    result.Error,
			Timestamp:       time.Now(),
		}
		NotifyAllHealthCheck(ctx, s.notifiers, alert)
	}
}
