package store

import (
	"context"
	"time"
)

// TraceStore manages distributed trace reassembly status.
type TraceStore interface {
	UpsertTraceStatus(ctx context.Context, traceID string, entry LogEntry) error
	GetTraceStatus(ctx context.Context, traceID string) (*TraceStatus, error)
	ListRecentTraces(ctx context.Context, limit, offset int) ([]TraceStatus, int, error)
	MarkStaleTraces(ctx context.Context, olderThan time.Duration) (int, error)
}
