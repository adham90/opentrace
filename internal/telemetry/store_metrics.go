package telemetry

import (
	"sync"
	"sync/atomic"
	"time"
)

// StoreMetrics tracks query latency for store operations.
type StoreMetrics struct {
	mu       sync.RWMutex
	counters map[string]*OperationMetrics
}

// OperationMetrics holds latency stats for a single operation.
type OperationMetrics struct {
	Name       string  `json:"name"`
	CallCount  int64   `json:"call_count"`
	ErrorCount int64   `json:"error_count"`
	TotalMs    int64   `json:"total_ms"`
	MaxMs      int64   `json:"max_ms"`
	AvgMs      float64 `json:"avg_ms"`
}

// NewStoreMetrics creates a store metrics tracker.
func NewStoreMetrics() *StoreMetrics {
	return &StoreMetrics{
		counters: make(map[string]*OperationMetrics),
	}
}

// Record records a single store operation.
func (s *StoreMetrics) Record(operation string, duration time.Duration, err error) {
	ms := duration.Milliseconds()

	s.mu.Lock()
	m, ok := s.counters[operation]
	if !ok {
		m = &OperationMetrics{Name: operation}
		s.counters[operation] = m
	}
	s.mu.Unlock()

	atomic.AddInt64(&m.CallCount, 1)
	atomic.AddInt64(&m.TotalMs, ms)
	if err != nil {
		atomic.AddInt64(&m.ErrorCount, 1)
	}

	// Update max (non-atomic but close enough for metrics).
	if ms > atomic.LoadInt64(&m.MaxMs) {
		atomic.StoreInt64(&m.MaxMs, ms)
	}
}

// Snapshot returns current metrics for all operations.
func (s *StoreMetrics) Snapshot() []OperationMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]OperationMetrics, 0, len(s.counters))
	for _, m := range s.counters {
		calls := atomic.LoadInt64(&m.CallCount)
		totalMs := atomic.LoadInt64(&m.TotalMs)
		avgMs := float64(0)
		if calls > 0 {
			avgMs = float64(totalMs) / float64(calls)
		}
		out = append(out, OperationMetrics{
			Name:       m.Name,
			CallCount:  calls,
			ErrorCount: atomic.LoadInt64(&m.ErrorCount),
			TotalMs:    totalMs,
			MaxMs:      atomic.LoadInt64(&m.MaxMs),
			AvgMs:      avgMs,
		})
	}
	return out
}

// Reset clears all metrics.
func (s *StoreMetrics) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counters = make(map[string]*OperationMetrics)
}
