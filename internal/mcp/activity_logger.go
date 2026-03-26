package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ActivityLogger provides bounded async logging for MCP tool calls.
// It uses a buffered channel and a fixed number of worker goroutines
// to prevent unbounded goroutine growth under heavy MCP usage.
type ActivityLogger struct {
	ch    chan store.LogMCPActivityParams
	store store.MCPActivityStore
	ctx   context.Context
	wg    sync.WaitGroup
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

// Log enqueues an activity log entry. If the buffer is full, the entry is dropped.
func (al *ActivityLogger) Log(params store.LogMCPActivityParams) {
	select {
	case al.ch <- params:
	default:
		// Channel full — drop rather than block tool execution
	}
}

func (al *ActivityLogger) worker() {
	defer al.wg.Done()
	for params := range al.ch {
		ctx, cancel := context.WithTimeout(al.ctx, 5*time.Second)
		_ = al.store.Log(ctx, params)
		cancel()
	}
}

// Close shuts down the logger, draining remaining entries.
// It waits for all workers to finish processing before returning.
func (al *ActivityLogger) Close() {
	close(al.ch)
	al.wg.Wait()
}
