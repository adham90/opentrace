package web

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// IngestQueue buffers incoming log entries and flushes them in batches to reduce
// write contention on SQLite (which allows only a single writer at a time).
// Entries are flushed when: the buffer reaches maxBatchSize, the flush interval
// fires, or Flush()/Stop() is called explicitly.
type IngestQueue struct {
	logStore      store.LogStore
	maxQueueSize  int
	maxBatchSize  int
	flushInterval time.Duration

	mu      sync.Mutex
	buffer  []store.LogEntry
	stopCh  chan struct{}
	stopped bool
	wg      sync.WaitGroup

	// Metrics (accessed atomically)
	flushCount    atomic.Int64
	overflowCount atomic.Int64
}

// IngestQueueConfig holds configuration for the IngestQueue.
type IngestQueueConfig struct {
	MaxQueueSize  int           // Maximum entries buffered before overflow fallback (default 10000)
	MaxBatchSize  int           // Maximum entries per flush batch (default 1000)
	FlushInterval time.Duration // Timer-based flush interval (default 100ms)
}

// NewIngestQueue creates and starts a new IngestQueue with the given configuration.
// The queue starts a background goroutine that flushes on a timer.
func NewIngestQueue(logStore store.LogStore, cfg IngestQueueConfig) *IngestQueue {
	if cfg.MaxQueueSize <= 0 {
		cfg.MaxQueueSize = 10000
	}
	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = 1000
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 100 * time.Millisecond
	}

	q := &IngestQueue{
		logStore:      logStore,
		maxQueueSize:  cfg.MaxQueueSize,
		maxBatchSize:  cfg.MaxBatchSize,
		flushInterval: cfg.FlushInterval,
		buffer:        make([]store.LogEntry, 0, cfg.MaxBatchSize),
		stopCh:        make(chan struct{}),
	}

	q.wg.Add(1)
	go q.flushLoop()

	return q
}

// Enqueue adds entries to the buffer. It returns immediately (non-blocking for
// the HTTP handler). If the queue is full, it falls back to synchronous insert
// so no data is lost.
func (q *IngestQueue) Enqueue(ctx context.Context, entries []store.LogEntry) (int, error) {
	q.mu.Lock()

	if q.stopped {
		q.mu.Unlock()
		// Queue is stopped; fall back to synchronous insert
		return q.logStore.BatchInsert(ctx, entries)
	}

	spaceAvailable := q.maxQueueSize - len(q.buffer)

	if spaceAvailable >= len(entries) {
		// All entries fit in the queue
		q.buffer = append(q.buffer, entries...)
		depth := len(q.buffer)
		shouldFlush := depth >= q.maxBatchSize
		q.mu.Unlock()

		slog.Debug("ingest_queue: enqueued entries", "count", len(entries), "queue_depth", depth)

		if shouldFlush {
			q.Flush()
		}
		return len(entries), nil
	}

	// Queue is full or partially full - enqueue what we can, sync-insert the rest
	q.overflowCount.Add(1)

	var enqueued []store.LogEntry
	var overflow []store.LogEntry

	if spaceAvailable > 0 {
		enqueued = entries[:spaceAvailable]
		overflow = entries[spaceAvailable:]
		q.buffer = append(q.buffer, enqueued...)
	} else {
		overflow = entries
	}
	q.mu.Unlock()

	slog.Warn("ingest_queue: queue full, falling back to synchronous insert",
		"enqueued", len(enqueued),
		"overflow", len(overflow),
		"overflow_total", q.overflowCount.Load(),
	)

	// Flush the queue to make room
	q.Flush()

	// Synchronous fallback for overflow entries
	count, err := q.logStore.BatchInsert(ctx, overflow)
	return len(enqueued) + count, err
}

// Flush drains the buffer and writes all buffered entries to the store.
func (q *IngestQueue) Flush() {
	q.mu.Lock()
	if len(q.buffer) == 0 {
		q.mu.Unlock()
		return
	}

	// Take ownership of the buffer and replace it
	batch := q.buffer
	q.buffer = make([]store.LogEntry, 0, q.maxBatchSize)
	q.mu.Unlock()

	q.flushBatch(batch)
}

// flushBatch writes a batch of entries to the store. It splits into chunks of
// maxBatchSize if needed.
func (q *IngestQueue) flushBatch(entries []store.LogEntry) {
	for len(entries) > 0 {
		end := q.maxBatchSize
		if end > len(entries) {
			end = len(entries)
		}
		chunk := entries[:end]
		entries = entries[end:]

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		count, err := q.logStore.BatchInsert(ctx, chunk)
		cancel()

		q.flushCount.Add(1)

		if err != nil {
			slog.Error("ingest_queue: flush failed",
				"error", err,
				"batch_size", len(chunk),
				"flush_count", q.flushCount.Load(),
			)
		} else {
			slog.Debug("ingest_queue: flushed batch",
				"count", count,
				"flush_count", q.flushCount.Load(),
			)
		}
	}
}

// flushLoop runs in the background and triggers flushes on a timer.
func (q *IngestQueue) flushLoop() {
	defer q.wg.Done()

	ticker := time.NewTicker(q.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			q.Flush()
		case <-q.stopCh:
			// Final flush on shutdown
			q.Flush()
			return
		}
	}
}

// Stop gracefully shuts down the queue: it marks the queue as stopped,
// signals the flush loop to exit, and waits for remaining entries to be flushed.
func (q *IngestQueue) Stop() {
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return
	}
	q.stopped = true
	q.mu.Unlock()

	close(q.stopCh)
	q.wg.Wait()

	slog.Info("ingest_queue: stopped",
		"flush_count", q.flushCount.Load(),
		"overflow_count", q.overflowCount.Load(),
	)
}

// QueueDepth returns the current number of buffered entries.
func (q *IngestQueue) QueueDepth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.buffer)
}

// FlushCount returns the total number of flush operations performed.
func (q *IngestQueue) FlushCount() int64 {
	return q.flushCount.Load()
}

// OverflowCount returns the total number of times the queue overflowed
// and entries were inserted synchronously.
func (q *IngestQueue) OverflowCount() int64 {
	return q.overflowCount.Load()
}
