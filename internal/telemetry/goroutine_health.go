package telemetry

import (
	"sync"
	"time"
)

// GoroutineHealth tracks the health of background goroutines.
type GoroutineHealth struct {
	mu      sync.RWMutex
	workers map[string]*WorkerStatus
}

// WorkerStatus tracks a single background worker's health.
type WorkerStatus struct {
	Name        string    `json:"name"`
	StartedAt   time.Time `json:"started_at"`
	LastPingAt  time.Time `json:"last_ping_at"`
	PingCount   int64     `json:"ping_count"`
	ErrorCount  int64     `json:"error_count"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitempty"`
}

// NewGoroutineHealth creates a goroutine health tracker.
func NewGoroutineHealth() *GoroutineHealth {
	return &GoroutineHealth{
		workers: make(map[string]*WorkerStatus),
	}
}

// Register records a new background worker starting.
func (g *GoroutineHealth) Register(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.workers[name] = &WorkerStatus{
		Name:      name,
		StartedAt: time.Now(),
	}
}

// Ping records a heartbeat from a background worker.
func (g *GoroutineHealth) Ping(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w, ok := g.workers[name]
	if !ok {
		w = &WorkerStatus{Name: name, StartedAt: time.Now()}
		g.workers[name] = w
	}
	w.LastPingAt = time.Now()
	w.PingCount++
}

// RecordError records an error from a background worker.
func (g *GoroutineHealth) RecordError(name string, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w, ok := g.workers[name]
	if !ok {
		w = &WorkerStatus{Name: name, StartedAt: time.Now()}
		g.workers[name] = w
	}
	w.ErrorCount++
	w.LastError = err.Error()
	w.LastErrorAt = time.Now()
}

// Status returns health status for all workers.
func (g *GoroutineHealth) Status() []WorkerStatus {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]WorkerStatus, 0, len(g.workers))
	for _, w := range g.workers {
		out = append(out, *w)
	}
	return out
}

// IsHealthy returns true if all workers have pinged within the given window.
func (g *GoroutineHealth) IsHealthy(maxStaleness time.Duration) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	now := time.Now()
	for _, w := range g.workers {
		if w.PingCount > 0 && now.Sub(w.LastPingAt) > maxStaleness {
			return false
		}
	}
	return true
}
