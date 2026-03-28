// Package logs provides the domain service for log search, analysis, and
// trace correlation. The repository interface defines what the service needs
// from a data store — implemented by adapter/sqlite in production.
package logs

import (
	"context"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// Repository defines data access for log operations.
// Implemented by internal/db.LogStore (SQLite adapter).
type Repository interface {
	Search(ctx context.Context, params store.LogSearchParams) ([]store.LogEntry, error)
	GetByID(ctx context.Context, id int64) (*store.LogEntry, error)
	CountByLevel(ctx context.Context, params store.LogCountParams) (map[string]int, error)
	CountByService(ctx context.Context, params store.LogCountParams) ([]store.ServiceLogCount, error)
	Histogram(ctx context.Context, params store.LogHistogramParams) ([]store.LogHistogramBucket, error)
	DistinctValues(ctx context.Context, field string, params store.LogCountParams) ([]string, error)
	MetadataKeys(ctx context.Context, params store.LogCountParams) ([]string, error)
	SearchRequestSummaries(ctx context.Context, params store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error)
}

// IngestRepository defines write operations for log ingestion.
// Separated from the read Repository to enforce read/write separation.
type IngestRepository interface {
	BatchInsert(ctx context.Context, entries []store.LogEntry) (int, error)
	RecordBatch(ctx context.Context, batchID string, logCount int) error
	GetBatch(ctx context.Context, batchID string) (*store.BatchRecord, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
	PruneBatches(ctx context.Context, olderThan time.Duration) (int64, error)
}
