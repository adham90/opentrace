package store

import (
	"time"

	"github.com/uptrace/bun"
)

// TraceStatus tracks the reassembly status of a distributed trace.
type TraceStatus struct {
	bun.BaseModel `bun:"table:trace_status" json:"-"`
	TraceID       string    `bun:"trace_id,pk" json:"trace_id"`
	SpanCount     int       `bun:"span_count" json:"span_count"`
	RootSpanID    string    `bun:"root_span_id" json:"root_span_id,omitempty"`
	Services      []string  `bun:"services" json:"services"`
	FirstSeenAt   time.Time `bun:"first_seen_at" json:"first_seen_at"`
	LastUpdatedAt time.Time `bun:"last_updated_at" json:"last_updated_at"`
	DurationMs    float64   `bun:"duration_ms" json:"duration_ms"`
	Status        string    `bun:"status" json:"status"` // "partial", "complete", "timeout"
	HasErrors     bool      `bun:"has_errors" json:"has_errors"`
}
