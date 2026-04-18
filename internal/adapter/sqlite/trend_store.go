package sqlite

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type trendStore struct {
	db *bun.DB
}

// NewTrendStore creates a TrendStore backed by SQLite.
func NewTrendStore(db *bun.DB) store.TrendStore {
	return &trendStore{db: db}
}

// aggregationDisabledOnce logs one startup INFO explaining that the
// trend/analytics aggregators are disabled because their source tables
// (logs, request_summaries) moved to the segmented log store.
var aggregationDisabledOnce sync.Once

func noteAggregationDisabled() {
	aggregationDisabledOnce.Do(func() {
		slog.Info("trend/analytics aggregation disabled",
			"reason", "source tables (logs, request_summaries) live in the segmented log store; aggregation needs to be ported to LogStore")
	})
}

// AggregateBuckets is a no-op. It previously read the `logs` and
// `request_summaries` SQLite tables and wrote rolled-up metric_buckets
// rows. Those source tables were removed when log storage moved to the
// segmented log store; re-enabling requires porting the aggregation to
// the LogStore interface.
func (s *trendStore) AggregateBuckets(_ context.Context, _ string, _ time.Time) error {
	noteAggregationDisabled()
	return nil
}

// QueryTrends returns metric buckets matching the given filters.
func (s *trendStore) QueryTrends(ctx context.Context, params store.TrendQueryParams) ([]store.MetricBucket, error) {
	var conditions []string
	var args []any

	interval := params.Interval
	if interval == "" {
		interval = "1h"
	}
	conditions = append(conditions, "bucket_interval = ?")
	args = append(args, interval)

	conditions = append(conditions, "bucket_start >= ?")
	args = append(args, params.Since.Format(time.RFC3339))

	conditions = append(conditions, "bucket_start <= ?")
	args = append(args, params.Until.Format(time.RFC3339))

	if params.Service != "" {
		conditions = append(conditions, "service = ?")
		args = append(args, params.Service)
	}
	if params.Endpoint != "" {
		conditions = append(conditions, "endpoint = ?")
		args = append(args, params.Endpoint)
	}
	if params.Environment != "" {
		conditions = append(conditions, "environment = ?")
		args = append(args, params.Environment)
	}

	query := `SELECT id, bucket_start, bucket_interval, service, endpoint, environment,
		request_count, error_count, log_count,
		avg_duration_ms, p50_duration_ms, p95_duration_ms, p99_duration_ms, max_duration_ms,
		avg_sql_count, avg_db_time_ms, avg_cache_hit_ratio, avg_http_external_ms, created_at
		FROM metric_buckets`

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY bucket_start ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying trends: %w", err)
	}
	defer rows.Close()

	var results []store.MetricBucket
	for rows.Next() {
		var b store.MetricBucket
		var bucketStartStr, createdAtStr string
		if err := rows.Scan(&b.ID, &bucketStartStr, &b.BucketInterval, &b.Service, &b.Endpoint, &b.Environment,
			&b.RequestCount, &b.ErrorCount, &b.LogCount,
			&b.AvgDurationMs, &b.P50DurationMs, &b.P95DurationMs, &b.P99DurationMs, &b.MaxDurationMs,
			&b.AvgSQLCount, &b.AvgDBTimeMs, &b.AvgCacheHitRatio, &b.AvgHTTPExternalMs, &createdAtStr); err != nil {
			return nil, fmt.Errorf("scanning metric bucket: %w", err)
		}
		b.BucketStart, _ = time.Parse(time.RFC3339, bucketStartStr)
		b.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		results = append(results, b)
	}
	return results, rows.Err()
}

// ListDeployMarkers returns deploy markers for a service since a given time.
func (s *trendStore) ListDeployMarkers(ctx context.Context, service string, since time.Time) ([]store.DeployMarker, error) {
	sinceStr := since.Format(time.RFC3339)

	type row struct {
		ID           int64  `bun:"id"`
		Service      string `bun:"service"`
		Environment  string `bun:"environment"`
		CommitHash   string `bun:"commit_hash"`
		FirstSeenAt  string `bun:"first_seen_at"`
		RequestCount int    `bun:"request_count"`
	}

	var query string
	var args []any
	if service != "" {
		query = `SELECT id, service, environment, commit_hash, first_seen_at, request_count
			FROM deploy_markers WHERE first_seen_at >= ? AND service = ? ORDER BY first_seen_at DESC`
		args = []any{sinceStr, service}
	} else {
		query = `SELECT id, service, environment, commit_hash, first_seen_at, request_count
			FROM deploy_markers WHERE first_seen_at >= ? ORDER BY first_seen_at DESC`
		args = []any{sinceStr}
	}

	var rows []row
	err := s.db.NewRaw(query, args...).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("querying deploy markers: %w", err)
	}

	results := make([]store.DeployMarker, len(rows))
	for i, r := range rows {
		results[i] = store.DeployMarker{
			ID: r.ID, Service: r.Service, Environment: r.Environment,
			CommitHash: r.CommitHash, FirstSeenAt: parseTime(r.FirstSeenAt),
			RequestCount: r.RequestCount,
		}
	}
	return results, nil
}

// Prune removes old metric buckets and deploy markers.
func (s *trendStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var total int64

	res, err := s.db.NewRaw(`DELETE FROM metric_buckets WHERE bucket_start < ?`, cutoff).Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("pruning metric buckets: %w", err)
	}
	n, _ := res.RowsAffected()
	total += n

	res, err = s.db.NewRaw(`DELETE FROM deploy_markers WHERE first_seen_at < ?`, cutoff).Exec(ctx)
	if err != nil {
		return total, fmt.Errorf("pruning deploy markers: %w", err)
	}
	n, _ = res.RowsAffected()
	total += n

	return total, nil
}
