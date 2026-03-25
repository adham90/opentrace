package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
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

// Worker polls a queue and dispatches jobs to registered handlers.
type Worker struct {
	queue        *Queue
	handlers     map[string]HandlerFunc
	queueName    string
	pollInterval time.Duration
	wg           sync.WaitGroup
	cancel       context.CancelFunc
}

// NewWorker creates a Worker with the given options.
func NewWorker(queue *Queue, opts ...WorkerOption) *Worker {
	w := &Worker{
		queue:        queue,
		handlers:     make(map[string]HandlerFunc),
		queueName:    "default",
		pollInterval: time.Second,
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

	handler, ok := w.handlers[job.JobType]
	if !ok {
		slog.Warn("unknown job type", "job_id", job.ID, "job_type", job.JobType)
		if failErr := w.queue.Fail(ctx, job.ID, fmt.Errorf("unknown job type: %s", job.JobType)); failErr != nil {
			slog.Error("failing unknown job", "job_id", job.ID, "error", failErr)
		}
		return
	}

	slog.Info("processing job", "job_id", job.ID, "job_type", job.JobType)

	if handlerErr := handler(ctx, job.Payload); handlerErr != nil {
		slog.Warn("job failed", "job_id", job.ID, "job_type", job.JobType, "error", handlerErr)
		if failErr := w.queue.Fail(ctx, job.ID, handlerErr); failErr != nil {
			slog.Error("recording job failure", "job_id", job.ID, "error", failErr)
		}
		return
	}

	if completeErr := w.queue.Complete(ctx, job.ID); completeErr != nil {
		slog.Error("completing job", "job_id", job.ID, "error", completeErr)
	}
}
