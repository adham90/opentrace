package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/safe"
)

const (
	// bookkeepingTimeout bounds the Complete/Fail writes that run on a context
	// detached from the (possibly already cancelled) poll context.
	bookkeepingTimeout = 15 * time.Second

	// defaultReapInterval is how often the worker reclaims jobs wedged in
	// 'running' by a failed bookkeeping write or a crashed process.
	defaultReapInterval = 5 * time.Minute

	// defaultVisibilityTimeout is how long a job may stay 'running' before the
	// periodic reap treats it as orphaned. It must comfortably exceed the
	// longest expected handler runtime.
	defaultVisibilityTimeout = 30 * time.Minute
)

// HandlerFunc processes a job's payload. Return nil on success, an error to trigger retry/failure.
type HandlerFunc func(ctx context.Context, payload json.RawMessage) error

// WorkerOption configures a Worker.
type WorkerOption func(*Worker)

// WithQueueName sets the queue name the worker polls (default "default").
func WithQueueName(name string) WorkerOption {
	return func(w *Worker) {
		w.queueName = name
	}
}

// WithPollInterval sets how often the worker checks for new jobs (default 1s).
func WithPollInterval(d time.Duration) WorkerOption {
	return func(w *Worker) {
		w.pollInterval = d
	}
}

// WithReapInterval sets how often the worker reclaims jobs wedged in 'running'.
// A non-positive value disables the periodic reap.
func WithReapInterval(d time.Duration) WorkerOption {
	return func(w *Worker) {
		w.reapInterval = d
	}
}

// WithVisibilityTimeout sets how long a job may stay 'running' before the
// periodic reap treats it as orphaned.
func WithVisibilityTimeout(d time.Duration) WorkerOption {
	return func(w *Worker) {
		w.visibilityTimeout = d
	}
}

// Worker polls a queue and dispatches jobs to registered handlers.
type Worker struct {
	queue             *Queue
	handlers          map[string]HandlerFunc
	queueName         string
	pollInterval      time.Duration
	reapInterval      time.Duration
	visibilityTimeout time.Duration
	wg                sync.WaitGroup
	cancel            context.CancelFunc
}

// NewWorker creates a Worker with the given options.
func NewWorker(queue *Queue, opts ...WorkerOption) *Worker {
	w := &Worker{
		queue:             queue,
		handlers:          make(map[string]HandlerFunc),
		queueName:         "default",
		pollInterval:      time.Second,
		reapInterval:      defaultReapInterval,
		visibilityTimeout: defaultVisibilityTimeout,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Register adds a handler for the given job type.
func (w *Worker) Register(jobType string, handler HandlerFunc) {
	w.handlers[jobType] = handler
}

// Start begins polling for jobs in a background goroutine.
// The worker stops when the context is cancelled or Stop is called.
func (w *Worker) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)
	w.wg.Add(1)
	go w.poll(ctx)

	if w.reapInterval > 0 && w.visibilityTimeout > 0 {
		w.wg.Add(1)
		go w.reapLoop(ctx)
	}
}

// reapLoop periodically reclaims jobs stuck in 'running' past the visibility
// timeout. Without it a single failed Complete/Fail write wedges the row and
// the scheduler skips that job type for the rest of the process's lifetime.
func (w *Worker) reapLoop(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.reapInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := w.queue.ReapOrphaned(ctx, w.visibilityTimeout)
			if err != nil {
				slog.Error("reaping orphaned jobs", "queue", w.queueName, "error", err)
				continue
			}
			if n > 0 {
				slog.Warn("reclaimed orphaned jobs", "queue", w.queueName, "count", n)
			}
		}
	}
}

// Stop cancels polling and waits for the current job to finish.
func (w *Worker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
}

func (w *Worker) poll(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processNext(ctx)
		}
	}
}

func (w *Worker) processNext(ctx context.Context) {
	job, err := w.queue.ClaimNext(ctx, w.queueName)
	if err != nil {
		slog.Error("claiming job", "queue", w.queueName, "error", err)
		return
	}
	if job == nil {
		return
	}

	// Bookkeeping writes must survive shutdown: the poll ctx is cancelled before
	// Stop() waits for the in-flight job, so using it here would leave a finished
	// job wedged in 'running' and re-execute it after the next boot's reap.
	bookkeepingCtx, cancelBookkeeping := context.WithTimeout(
		context.WithoutCancel(ctx), bookkeepingTimeout)
	defer cancelBookkeeping()

	handler, ok := w.handlers[job.JobType]
	if !ok {
		slog.Warn("unknown job type", "job_id", job.ID, "job_type", job.JobType)
		if failErr := w.queue.Fail(bookkeepingCtx, job.ID, fmt.Errorf("unknown job type: %s", job.JobType)); failErr != nil {
			slog.Error("failing unknown job", "job_id", job.ID, "error", failErr)
		}
		return
	}

	slog.Info("processing job", "job_id", job.ID, "job_type", job.JobType)

	handlerErr := safe.Call("job:"+job.JobType, func() error { return handler(ctx, job.Payload) })
	if handlerErr != nil {
		slog.Warn("job failed", "job_id", job.ID, "job_type", job.JobType, "error", handlerErr)
		if failErr := w.queue.Fail(bookkeepingCtx, job.ID, handlerErr); failErr != nil {
			slog.Error("recording job failure", "job_id", job.ID, "error", failErr)
		}
		return
	}

	if completeErr := w.queue.Complete(bookkeepingCtx, job.ID); completeErr != nil {
		// The row stays 'running' and will be reclaimed by the periodic reap.
		// Jobs are at-least-once: the handler may run again.
		slog.Error("completing job", "job_id", job.ID, "error", completeErr)
	}
}
