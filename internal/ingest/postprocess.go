package ingest

import (
	"context"
	"sync"

	"github.com/adham90/opentrace/internal/safe"
	"github.com/adham90/opentrace/pkg/store"
)

type postProcessor struct {
	handler *Handler
	jobs    chan []store.LogEntry
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
			}
		}()
	}
	h.postProcessor = p
}

func (h *Handler) enqueuePostprocess(entries []store.LogEntry) {
	h.postMu.RLock()
	p := h.postProcessor
	if p != nil {
		select {
		case p.jobs <- entries:
			h.postMu.RUnlock()
			return
		default:
		}
	}
	h.postMu.RUnlock()

	// No pool (unit tests/embedded use) or a full queue: preserve every
	// side-effect and bound memory by doing the work on the caller.
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
