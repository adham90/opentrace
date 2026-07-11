// Package safe provides panic-isolated helpers for background work. A panic in
// a bare goroutine (job handler, ingest side-effect, health probe, watch eval)
// takes down the entire process — these helpers recover, log with a stack, and
// let the failing unit die alone.
package safe

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Go runs fn in a new goroutine, recovering and logging any panic.
func Go(task string, fn func()) {
	go Run(task, fn)
}

// Run executes fn on the current goroutine, recovering and logging any panic.
// Use inside an existing loop (e.g. a worker) for per-iteration protection.
func Run(task string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered from panic in background task",
				"task", task, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	fn()
}

// Call executes fn and converts a panic into an error so callers (e.g. the job
// worker) can record the failure instead of crashing.
func Call(task string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered from panic in background task",
				"task", task, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("panic in %s: %v", task, r)
		}
	}()
	return fn()
}
