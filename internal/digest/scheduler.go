package digest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// SchedulerOpts configures the digest scheduler.
type SchedulerOpts struct {
	Builder       *Builder
	DigestStore   store.DigestStore
	Schedule      string // cron expression (unused for now — uses interval)
	RetentionDays int
	Environments  []string // if empty, generates a single digest with no env filter
}

// Scheduler generates digests on a fixed interval and cleans up old ones.
type Scheduler struct {
	builder       *Builder
	digestStore   store.DigestStore
	retentionDays int
	environments  []string

	mu       sync.Mutex
	running  bool
	cancelFn context.CancelFunc
}

// NewScheduler creates a new digest Scheduler.
func NewScheduler(opts SchedulerOpts) *Scheduler {
	retention := opts.RetentionDays
	if retention <= 0 {
		retention = 30
	}
	return &Scheduler{
		builder:       opts.Builder,
		digestStore:   opts.DigestStore,
		retentionDays: retention,
		environments:  opts.Environments,
	}
}

// GenerateAndStore generates a digest for the last 24 hours and stores it.
// Can be called on-demand or by the scheduling loop.
func (s *Scheduler) GenerateAndStore(ctx context.Context, environment string) error {
	now := time.Now().UTC()
	periodStart := now.Add(-24 * time.Hour)

	d, err := s.builder.Generate(ctx, DigestOpts{
		PeriodStart: periodStart,
		PeriodEnd:   now,
		Environment: environment,
		TopN:        10,
	})
	if err != nil {
		return fmt.Errorf("generating digest: %w", err)
	}

	data, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("marshaling digest: %w", err)
	}

	err = s.digestStore.Save(ctx, store.SaveDigestParams{
		ID:             d.ID,
		Environment:    environment,
		Status:         string(d.Status),
		PeriodStart:    d.PeriodStart,
		PeriodEnd:      d.PeriodEnd,
		AlertTotal:     d.AlertSummary.Total,
		AlertCritical:  d.AlertSummary.Critical,
		AlertWarning:   d.AlertSummary.Warning,
		MonitorTotal:   d.MonitorSummary.Total,
		MonitorErrored: d.MonitorSummary.InError,
		FailedRuns:     d.MonitorSummary.FailedRuns,
		Data:           string(data),
	})
	if err != nil {
		return fmt.Errorf("storing digest: %w", err)
	}

	return nil
}

// Start begins the scheduling loop. It generates digests once per day at midnight UTC.
// For more advanced cron scheduling, integrate with Plan 004 when available.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	ctx, s.cancelFn = context.WithCancel(ctx)
	s.mu.Unlock()

	go s.loop(ctx)
}

// Stop gracefully stops the scheduler.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelFn != nil {
		s.cancelFn()
		s.cancelFn = nil
	}
	s.running = false
}

func (s *Scheduler) loop(ctx context.Context) {
	// Generate immediately on startup
	s.generateAll(ctx)

	// Then tick every hour, generating a new digest once per day at midnight UTC
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	lastGenDay := time.Now().UTC().YearDay()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			today := now.UTC().YearDay()
			if today != lastGenDay {
				s.generateAll(ctx)
				s.cleanup(ctx)
				lastGenDay = today
			}
		}
	}
}

func (s *Scheduler) generateAll(ctx context.Context) {
	if len(s.environments) == 0 {
		if err := s.GenerateAndStore(ctx, ""); err != nil {
			log.Printf("digest generation error: %v", err)
		} else {
			log.Println("daily digest generated")
		}
		return
	}

	for _, env := range s.environments {
		if err := s.GenerateAndStore(ctx, env); err != nil {
			log.Printf("digest generation error (env=%s): %v", env, err)
		} else {
			log.Printf("daily digest generated (env=%s)", env)
		}
	}
}

func (s *Scheduler) cleanup(ctx context.Context) {
	cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
	deleted, err := s.digestStore.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		log.Printf("digest cleanup error: %v", err)
		return
	}
	if deleted > 0 {
		log.Printf("cleaned up %d old digest(s)", deleted)
	}
}
