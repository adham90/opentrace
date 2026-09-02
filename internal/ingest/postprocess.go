package ingest

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/adham90/opentrace/internal/safe"
	"github.com/adham90/opentrace/pkg/store"
)

// maxQueuedPostprocessEntries caps how many log entries may sit in the queue at
// once, across all jobs.
//
// The channel is sized in jobs, and a job is a whole ingest batch, so a queue
// depth of N bounds nothing useful: N full batches of entries-with-bodies is
// tens of megabytes that the pool holds while the caller returns 201 and moves
// on. Entry count is the thing that actually tracks the memory here, and this
// is the same bound the WAL cache applies for the same reason.
//
// ponytail: a byte bound would be tighter, but entries here are ingest-shaped
// and capped at the boundary, so counting them is within a small factor and
// costs one addition.
const maxQueuedPostprocessEntries = 5000

type postProcessor struct {
	handler *Handler
	jobs    chan []store.LogEntry
	queued  atomic.Int64
	wg      sync.WaitGroup
}

// StartPostProcessor starts a bounded side-effect pool. It is explicit rather
// than lazy so unit-test handlers do not leak background goroutines.
func (h *Handler) StartPostProcessor(workers, queueSize int) {
	if workers < 1 {
		workers = 1
	}
	if queueSize < 1 {
		queueSize = 1
	}

	h.postMu.Lock()
	defer h.postMu.Unlock()
	if h.postProcessor != nil {
		return
	}
	p := &postProcessor{handler: h, jobs: make(chan []store.LogEntry, queueSize)}
	p.wg.Add(workers)
	for range workers {
		go func() {
			defer p.wg.Done()
			for entries := range p.jobs {
				safe.Run("ingest.processAfterInsert", func() { h.processAfterInsert(entries) })
				p.queued.Add(-int64(len(entries)))
			}
		}()
	}
	h.postProcessor = p
}

func (h *Handler) enqueuePostprocess(entries []store.LogEntry) {
	h.postMu.RLock()
	p := h.postProcessor
	if p != nil && p.queued.Load()+int64(len(entries)) <= maxQueuedPostprocessEntries {
		select {
		case p.jobs <- entries:
			p.queued.Add(int64(len(entries)))
			h.postMu.RUnlock()
			return
		default:
		}
	}
	h.postMu.RUnlock()

	// No pool (unit tests/embedded use), a full queue, or too many entries
	// already waiting: preserve every side-effect and bound memory by doing the
	// work on the caller. Running it here is also the backpressure — the sender
	// waits for its own side effects instead of the queue growing without end.
	safe.Run("ingest.processAfterInsert", func() { h.processAfterInsert(entries) })
}

// StopPostProcessor closes the queue and waits for accepted jobs to drain.
func (h *Handler) StopPostProcessor(ctx context.Context) error {
	h.postMu.Lock()
	p := h.postProcessor
	if p == nil {
		h.postMu.Unlock()
		return nil
	}
	h.postProcessor = nil
	close(p.jobs)
	h.postMu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
