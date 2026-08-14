package ingest

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// flushMaxAttempts is how many times a chunk is retried before the queue gives
// up on it. Flush failures are dominated by transient contention (SQLITE_BUSY,
// a compacting engine), so a couple of quick retries recovers most of them.
const flushMaxAttempts = 3

// flushRetryBackoff is the pause between flush attempts. Kept short: the flush
// loop is the only writer and a long sleep would stall every later batch.
const flushRetryBackoff = 50 * time.Millisecond

// flushTimeout bounds a single BatchInsert attempt.
const flushTimeout = 30 * time.Second

// Queue buffers incoming log entries and flushes them in batches to reduce
// write contention on SQLite (which allows only a single writer at a time).
// Entries are flushed when: the buffer reaches maxBatchSize, the flush interval
// fires, or Flush()/Stop() is called explicitly.
//
// NOTE: nothing in cmd/ constructs a Queue — api.ServerDeps.IngestQueue is
// never populated, so Handler.Queue is nil in the real server and every request
// takes the synchronous insert path. This type is opt-in infrastructure kept
// for embedders that wire it explicitly; treat its behaviour as untested in
// production before enabling it.
type Queue struct {
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
	dropCount     atomic.Int64
}

// QueueConfig holds configuration for the Queue.
type QueueConfig struct {
	MaxQueueSize  int           // Maximum entries buffered before overflow fallback (default 10000)
	MaxBatchSize  int           // Maximum entries per flush batch (default 1000)
	FlushInterval time.Duration // Timer-based flush interval (default 100ms)
}

// NewQueue creates and starts a new Queue with the given configuration.
// The queue starts a background goroutine that flushes on a timer.
func NewQueue(logStore store.LogStore, cfg QueueConfig) *Queue {
	if cfg.MaxQueueSize <= 0 {
		cfg.MaxQueueSize = 10000
	}
	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = 1000
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 100 * time.Millisecond
	}

	q := &Queue{
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
// the HTTP handler). If the batch does not fit, the queue is flushed to make
// room and, if it still does not fit, the WHOLE batch is inserted synchronously.
//
// A batch is never split across the buffer and a synchronous insert: the split
// version left the buffered prefix persisted while returning the sync insert's
// error, so the caller reported a failure the client then retried, duplicating
// the prefix. All-or-nothing keeps the returned error an accurate statement
// about the entire batch.
func (q *Queue) Enqueue(ctx context.Context, entries []store.LogEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	q.mu.Lock()

	if q.stopped {
		q.mu.Unlock()
		// Queue is stopped; fall back to synchronous insert
		return q.logStore.BatchInsert(ctx, entries)
	}

	if enqueued, depth, ok := q.tryBuffer(entries); ok {
		q.mu.Unlock()
		slog.Debug("ingest_queue: enqueued entries", "count", enqueued, "queue_depth", depth)
		if depth >= q.maxBatchSize {
			q.Flush()
		}
		return enqueued, nil
	}
	q.mu.Unlock()

	q.overflowCount.Add(1)
	slog.Warn("ingest_queue: queue full, flushing before retry",
		"batch_size", len(entries),
		"overflow_total", q.overflowCount.Load(),
	)

	// Flush the queue to make room, then retry the whole batch.
	q.Flush()

	q.mu.Lock()
	if !q.stopped {
		if enqueued, depth, ok := q.tryBuffer(entries); ok {
			q.mu.Unlock()
			if depth >= q.maxBatchSize {
				q.Flush()
			}
			return enqueued, nil
		}
	}
	q.mu.Unlock()

	// Batch is larger than the whole queue: insert it synchronously, intact.
	slog.Warn("ingest_queue: batch exceeds queue capacity, inserting synchronously",
		"batch_size", len(entries), "max_queue_size", q.maxQueueSize)
	return q.logStore.BatchInsert(ctx, entries)
}

// tryBuffer appends entries to the buffer if they all fit. Caller holds q.mu.
func (q *Queue) tryBuffer(entries []store.LogEntry) (enqueued, depth int, ok bool) {
	if q.maxQueueSize-len(q.buffer) < len(entries) {
		return 0, len(q.buffer), false
	}
	q.buffer = append(q.buffer, entries...)
	return len(entries), len(q.buffer), true
}

// Flush drains the buffer and writes all buffered entries to the store.
func (q *Queue) Flush() {
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
// maxBatchSize if needed. A chunk that fails every attempt is put back on the
// buffer so a later flush retries it, and is only dropped when the buffer has
// no room left — previously a single failed insert silently discarded up to
// maxBatchSize entries that the handler had already acknowledged.
func (q *Queue) flushBatch(entries []store.LogEntry) {
	for len(entries) > 0 {
		end := q.maxBatchSize
		if end > len(entries) {
			end = len(entries)
		}
		chunk := entries[:end]
		entries = entries[end:]

		if err := q.insertWithRetry(chunk); err != nil {
			q.requeueOrDrop(chunk, err)
		}
	}
}

// insertWithRetry attempts a single chunk insert up to flushMaxAttempts times.
func (q *Queue) insertWithRetry(chunk []store.LogEntry) error {
	var err error
	for attempt := 1; attempt <= flushMaxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
		var count int
		count, err = q.logStore.BatchInsert(ctx, chunk)
		cancel()

		q.flushCount.Add(1)

		if err == nil {
			slog.Debug("ingest_queue: flushed batch",
				"count", count,
				"flush_count", q.flushCount.Load(),
			)
			return nil
		}

		slog.Warn("ingest_queue: flush attempt failed",
			"error", err,
			"attempt", attempt,
			"max_attempts", flushMaxAttempts,
			"batch_size", len(chunk),
		)
		if attempt < flushMaxAttempts {
			time.Sleep(flushRetryBackoff)
		}
	}
	return err
}

// requeueOrDrop puts a chunk that could not be written back on the buffer for a
// later flush, dropping it only when the buffer is full or the queue is closed.
func (q *Queue) requeueOrDrop(chunk []store.LogEntry, cause error) {
	q.mu.Lock()
	requeued := !q.stopped && q.maxQueueSize-len(q.buffer) >= len(chunk)
	if requeued {
		// Prepend: these entries are older than anything currently buffered.
		q.buffer = append(chunk[:len(chunk):len(chunk)], q.buffer...)
	}
	q.mu.Unlock()

	if requeued {
		slog.Warn("ingest_queue: flush failed, re-queued for retry",
			"error", cause, "batch_size", len(chunk))
		return
	}

	q.dropCount.Add(int64(len(chunk)))
	slog.Error("ingest_queue: flush failed and buffer is full, dropping entries",
		"error", cause,
		"dropped", len(chunk),
		"dropped_total", q.dropCount.Load(),
	)
}

// flushLoop runs in the background and triggers flushes on a timer.
func (q *Queue) flushLoop() {
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
func (q *Queue) Stop() {
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
		"drop_count", q.dropCount.Load(),
	)
}

// QueueDepth returns the current number of buffered entries.
func (q *Queue) QueueDepth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.buffer)
}

// FlushCount returns the total number of flush operations performed.
func (q *Queue) FlushCount() int64 {
	return q.flushCount.Load()
}

// OverflowCount returns the total number of times the queue overflowed
// and entries were inserted synchronously.
func (q *Queue) OverflowCount() int64 {
	return q.overflowCount.Load()
}

// DropCount returns the number of entries permanently lost because every flush
// attempt failed and the buffer had no room to hold them for another try.
func (q *Queue) DropCount() int64 {
	return q.dropCount.Load()
}
