package store

import "time"

// TraceStatus tracks the reassembly status of a distributed trace.
type TraceStatus struct {
	TraceID       string    `json:"trace_id"`
	SpanCount     int       `json:"span_count"`
	RootSpanID    string    `json:"root_span_id,omitempty"`
	Services      []string  `json:"services"`
	FirstSeenAt   time.Time `json:"first_seen_at"`
	LastUpdatedAt time.Time `json:"last_updated_at"`
	DurationMs    float64   `json:"duration_ms"`
	Status        string    `json:"status"` // "partial", "complete", "timeout"
	HasErrors     bool      `json:"has_errors"`
}
