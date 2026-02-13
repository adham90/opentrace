package mcp

import (
	"context"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// ActivityLogger provides bounded async logging for MCP tool calls.
// It uses a buffered channel and a fixed number of worker goroutines
// to prevent unbounded goroutine growth under heavy MCP usage.
type ActivityLogger struct {
	ch    chan store.LogMCPActivityParams
	store store.MCPActivityStore
	done  chan struct{}
}

// NewActivityLogger creates an ActivityLogger with the given buffer size and worker count.
// Workers drain the channel and write to the store. If the channel is full, new log
// entries are silently dropped to avoid blocking tool execution.
func NewActivityLogger(s store.MCPActivityStore, bufSize int, workers int) *ActivityLogger {
	al := &ActivityLogger{
		ch:    make(chan store.LogMCPActivityParams, bufSize),
		store: s,
		done:  make(chan struct{}),
	}
	for i := 0; i < workers; i++ {
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
	for params := range al.ch {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = al.store.Log(ctx, params)
		cancel()
	}
}

// Close shuts down the logger, draining remaining entries.
func (al *ActivityLogger) Close() {
	close(al.ch)
}
