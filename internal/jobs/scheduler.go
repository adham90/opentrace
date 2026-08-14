package jobs

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Schedule defines a recurring job that should be enqueued at a fixed interval.
type Schedule struct {
	Name     string
	JobType  string
	Payload  any
	Interval time.Duration
}

// Scheduler periodically enqueues jobs according to registered schedules.
type Scheduler struct {
	queue     *Queue
	schedules []Schedule
	wg        sync.WaitGroup
	cancel    context.CancelFunc
}

// NewScheduler creates a Scheduler backed by the given Queue.
func NewScheduler(queue *Queue) *Scheduler {
	return &Scheduler{
		queue: queue,
	}
}

// Add registers a schedule. Must be called before Start.
func (s *Scheduler) Add(schedule Schedule) {
	s.schedules = append(s.schedules, schedule)
}

// Start launches a goroutine for each registered schedule.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	for _, sched := range s.schedules {
		s.wg.Add(1)
		go s.run(ctx, sched)
	}
}

// Stop cancels all schedule goroutines and waits for them to finish.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

func (s *Scheduler) run(ctx context.Context, sched Schedule) {
	defer s.wg.Done()

	ticker := time.NewTicker(sched.Interval)
	defer ticker.Stop()

	// Fire once at startup. Without this a process that restarts more often than
	// the interval (e.g. a 6h retention schedule on a service redeployed hourly)
	// never reaches its first tick and the job never runs. maybeEnqueue is a
	// no-op when the job is already pending/running, so this is safe to repeat.
	s.maybeEnqueue(ctx, sched)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.maybeEnqueue(ctx, sched)
		}
	}
}

func (s *Scheduler) maybeEnqueue(ctx context.Context, sched Schedule) {
	var count int
	err := s.queue.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE job_type = ? AND status IN ('pending', 'running')`,
		sched.JobType,
	).Scan(&count)
	if err != nil {
		slog.Error("checking existing jobs", "schedule", sched.Name, "error", err)
		return
	}

	if count > 0 {
		slog.Debug("skipping scheduled job, already pending/running", "schedule", sched.Name, "job_type", sched.JobType)
		return
	}

	if _, err := s.queue.Enqueue(ctx, sched.JobType, sched.Payload); err != nil {
		slog.Error("enqueuing scheduled job", "schedule", sched.Name, "error", err)
		return
	}

	slog.Info("enqueued scheduled job", "schedule", sched.Name, "job_type", sched.JobType)
}
