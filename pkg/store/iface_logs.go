package store

import (
	"context"
	"time"
)

// LogStore defines operations for log ingestion and search.
type LogStore interface {
	BatchInsert(ctx context.Context, entries []LogEntry) (int, error)
	Search(ctx context.Context, params LogSearchParams) ([]LogEntry, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
	CountByLevel(ctx context.Context, params LogCountParams) (map[string]int, error)
	CountByService(ctx context.Context, params LogCountParams) ([]ServiceLogCount, error)
	Histogram(ctx context.Context, params LogHistogramParams) ([]LogHistogramBucket, error)
	DistinctValues(ctx context.Context, field string, params LogCountParams) ([]string, error)
	MetadataKeys(ctx context.Context, params LogCountParams) ([]string, error)
	GetByID(ctx context.Context, id int64) (*LogEntry, error)
	// Request performance
	SearchRequestSummaries(ctx context.Context, params RequestSummarySearchParams) ([]RequestSummaryResult, error)
	// Batch deduplication
	RecordBatch(ctx context.Context, batchID string, logCount int) error
	GetBatch(ctx context.Context, batchID string) (*BatchRecord, error)
	PruneBatches(ctx context.Context, olderThan time.Duration) (int64, error)
}

// BatchRecord tracks a processed batch for deduplication.
type BatchRecord struct {
	BatchID    string
	LogCount   int
	ReceivedAt time.Time
}
