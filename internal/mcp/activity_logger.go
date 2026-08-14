package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

const (
	// activityLogBuffer is how many pending activity rows are held before new
	// entries are dropped rather than blocking tool execution.
	activityLogBuffer = 256
	// activityLogWorkers is the number of goroutines draining the buffer.
	activityLogWorkers = 2
	// activityWriteTimeout bounds a single activity row write.
	activityWriteTimeout = 5 * time.Second
)

// ActivityLogger provides bounded async logging for MCP tool calls.
// It uses a buffered channel and a fixed number of worker goroutines
// to prevent unbounded goroutine growth under heavy MCP usage.
type ActivityLogger struct {
	ch    chan store.LogMCPActivityParams
	store store.MCPActivityStore
	ctx   context.Context
	wg    sync.WaitGroup

	// mu guards closed and serialises Log against Close so an in-flight tool
	// call can never send on a closed channel.
	mu     sync.RWMutex
	closed bool
}

// NewActivityLogger creates an ActivityLogger with the given buffer size and worker count.
// The provided ctx is used as the parent for per-write timeouts, so cancelling it
// (e.g. on app shutdown) immediately cancels any in-flight store writes.
// Workers drain the channel and write to the store. If the channel is full, new log
// entries are silently dropped to avoid blocking tool execution.
func NewActivityLogger(ctx context.Context, s store.MCPActivityStore, bufSize int, workers int) *ActivityLogger {
	al := &ActivityLogger{
		ch:    make(chan store.LogMCPActivityParams, bufSize),
		store: s,
		ctx:   ctx,
	}
	for i := 0; i < workers; i++ {
		al.wg.Add(1)
		go al.worker()
	}
	return al
}

// Log enqueues an activity log entry. If the buffer is full, or the logger has
// already been closed, the entry is dropped.
func (al *ActivityLogger) Log(params store.LogMCPActivityParams) {
	al.mu.RLock()
	defer al.mu.RUnlock()
	if al.closed {
		return
	}
	select {
	case al.ch <- params:
	default:
		// Channel full — drop rather than block tool execution
	}
}

func (al *ActivityLogger) worker() {
	defer al.wg.Done()
	for params := range al.ch {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(al.ctx), activityWriteTimeout)
		_ = al.store.Log(ctx, params)
		cancel()
	}
}

// closeOnDone drains and shuts the logger down when ctx is cancelled. It is
// how loggers created inside NewConfiguredServer — which receives deps by
// value and so cannot hand the logger back — still get shut down at app exit.
// A background ctx never fires, which is correct: nothing owns that logger.
func (al *ActivityLogger) closeOnDone(ctx context.Context) {
	if ctx == nil || ctx.Done() == nil {
		return
	}
	go func() {
		<-ctx.Done()
		al.Close()
	}()
}

// Close shuts down the logger, draining remaining entries.
// It waits for all workers to finish processing before returning.
// Safe to call more than once.
func (al *ActivityLogger) Close() {
	al.mu.Lock()
	if al.closed {
		al.mu.Unlock()
		return
	}
	al.closed = true
	close(al.ch)
	al.mu.Unlock()

	al.wg.Wait()
}
