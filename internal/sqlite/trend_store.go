package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type trendStore struct {
	db *sql.DB
}

// NewTrendStore creates a TrendStore backed by SQLite.
func NewTrendStore(db *sql.DB) store.TrendStore {
	return &trendStore{db: db}
}

// AggregateBuckets reads raw logs + request_summaries and populates metric_buckets
// for the given interval starting from `since`. It also detects new deploy markers.
func (s *trendStore) AggregateBuckets(ctx context.Context, interval string, since time.Time) error {
	bucketDur, err := parseBucketDuration(interval)
	if err != nil {
		return fmt.Errorf("invalid interval %q: %w", interval, err)
	}

	now := time.Now().UTC()
	// Align since to bucket boundary
	since = since.Truncate(bucketDur)

	for t := since; t.Before(now); t = t.Add(bucketDur) {
		bucketEnd := t.Add(bucketDur)
		if bucketEnd.After(now) {
			bucketEnd = now
		}

		start := t.Format(time.RFC3339)
		end := bucketEnd.Format(time.RFC3339)
		bucketStart := t.Format(time.RFC3339)

		// Aggregate logs: count by service, level
		rows, err := s.db.QueryContext(ctx, `
			SELECT COALESCE(l.service, '') as service,
			       COALESCE(l.environment, '') as env,
			       COUNT(*) as log_count,
			       SUM(CASE WHEN l.level IN ('error', 'fatal', 'ERROR', 'FATAL') THEN 1 ELSE 0 END) as error_count
			FROM logs l
			WHERE l.timestamp >= ? AND l.timestamp < ?
			GROUP BY service, env
		`, start, end)
		if err != nil {
			return fmt.Errorf("querying log counts: %w", err)
		}

		type logAgg struct {
			service    string
			env        string
			logCount   int
			errorCount int
		}
		var logAggs []logAgg
		for rows.Next() {
			var a logAgg
			if err := rows.Scan(&a.service, &a.env, &a.logCount, &a.errorCount); err != nil {
				rows.Close()
				return fmt.Errorf("scanning log agg: %w", err)
			}
			logAggs = append(logAggs, a)
		}
		rows.Close()

		// Aggregate request summaries: by service+endpoint
		rows2, err := s.db.QueryContext(ctx, `
			SELECT COALESCE(l.service, '') as service,
			       COALESCE(l.environment, '') as env,
			       COALESCE(rs.controller || '#' || rs.action, '') as endpoint,
			       COUNT(*) as request_count,
			       SUM(CASE WHEN rs.status >= 500 THEN 1 ELSE 0 END) as error_count,
			       AVG(rs.duration_ms) as avg_dur,
			       MAX(rs.duration_ms) as max_dur,
			       AVG(rs.sql_count) as avg_sql,
			       AVG(rs.db_time_ms) as avg_db,
			       AVG(rs.cache_hit_ratio) as avg_cache,
			       AVG(rs.http_external_total_ms) as avg_http
			FROM request_summaries rs
			JOIN logs l ON rs.log_id = l.id
			WHERE l.timestamp >= ? AND l.timestamp < ?
			GROUP BY service, env, endpoint
		`, start, end)
		if err != nil {
			return fmt.Errorf("querying request aggs: %w", err)
		}

		type reqAgg struct {
			service      string
			env          string
			endpoint     string
			requestCount int
			errorCount   int
			avgDur       float64
			maxDur       float64
			avgSQL       float64
			avgDB        float64
			avgCache     float64
			avgHTTP      float64
		}
		var reqAggs []reqAgg
		for rows2.Next() {
			var a reqAgg
			if err := rows2.Scan(&a.service, &a.env, &a.endpoint, &a.requestCount,
				&a.errorCount, &a.avgDur, &a.maxDur, &a.avgSQL, &a.avgDB, &a.avgCache, &a.avgHTTP); err != nil {
				rows2.Close()
				return fmt.Errorf("scanning request agg: %w", err)
			}
			reqAggs = append(reqAggs, a)
		}
		rows2.Close()

		// Compute percentiles per service+env (we need the raw durations)
		type percKey struct{ service, env string }
		percentiles := make(map[percKey]struct{ p50, p95, p99 float64 })

		percRows, err := s.db.QueryContext(ctx, `
			SELECT COALESCE(l.service, '') as service,
			       COALESCE(l.environment, '') as env,
			       rs.duration_ms
			FROM request_summaries rs
			JOIN logs l ON rs.log_id = l.id
			WHERE l.timestamp >= ? AND l.timestamp < ?
			ORDER BY service, env, rs.duration_ms
		`, start, end)
		if err == nil {
			durMap := make(map[percKey][]float64)
			for percRows.Next() {
				var svc, env string
				var dur float64
				if err := percRows.Scan(&svc, &env, &dur); err == nil {
					key := percKey{svc, env}
					durMap[key] = append(durMap[key], dur)
				}
			}
			percRows.Close()

			for key, durs := range durMap {
				sort.Float64s(durs)
				percentiles[key] = struct{ p50, p95, p99 float64 }{
					p50: percentile(durs, 50),
					p95: percentile(durs, 95),
					p99: percentile(durs, 99),
				}
			}
		}

		// Upsert metric buckets: one per service+env (aggregated across endpoints)
		// Merge logAggs and reqAggs by service+env
		type bucketKey struct{ service, env string }
		merged := make(map[bucketKey]*store.MetricBucket)

		for _, a := range logAggs {
			key := bucketKey{a.service, a.env}
			b, ok := merged[key]
			if !ok {
				bs, _ := time.Parse(time.RFC3339, bucketStart)
				b = &store.MetricBucket{
					BucketStart:    bs,
					BucketInterval: interval,
					Service:        a.service,
					Environment:    a.env,
				}
				merged[key] = b
			}
			b.LogCount = a.logCount
			b.ErrorCount += a.errorCount
		}

		for _, a := range reqAggs {
			key := bucketKey{a.service, a.env}
			b, ok := merged[key]
			if !ok {
				bs, _ := time.Parse(time.RFC3339, bucketStart)
				b = &store.MetricBucket{
					BucketStart:    bs,
					BucketInterval: interval,
					Service:        a.service,
					Environment:    a.env,
				}
				merged[key] = b
			}
			b.RequestCount += a.requestCount
			b.ErrorCount += a.errorCount
			// Use weighted average if there are multiple endpoints
			if b.AvgDurationMs == 0 {
				b.AvgDurationMs = a.avgDur
			} else {
				b.AvgDurationMs = (b.AvgDurationMs + a.avgDur) / 2
			}
			if a.maxDur > b.MaxDurationMs {
				b.MaxDurationMs = a.maxDur
			}
			b.AvgSQLCount = a.avgSQL
			b.AvgDBTimeMs = a.avgDB
			b.AvgCacheHitRatio = a.avgCache
			b.AvgHTTPExternalMs = a.avgHTTP
		}

		// Apply percentiles
		for key, b := range merged {
			pk := percKey{key.service, key.env}
			if p, ok := percentiles[pk]; ok {
				b.P50DurationMs = p.p50
				b.P95DurationMs = p.p95
				b.P99DurationMs = p.p99
			}
		}

		// Upsert all buckets
		for _, b := range merged {
			_, err := s.db.ExecContext(ctx, `
				INSERT INTO metric_buckets (
					bucket_start, bucket_interval, service, endpoint, environment,
					request_count, error_count, log_count,
					avg_duration_ms, p50_duration_ms, p95_duration_ms, p99_duration_ms, max_duration_ms,
					avg_sql_count, avg_db_time_ms, avg_cache_hit_ratio, avg_http_external_ms
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(bucket_start, bucket_interval, service, endpoint, environment) DO UPDATE SET
					request_count = excluded.request_count,
					error_count = excluded.error_count,
					log_count = excluded.log_count,
					avg_duration_ms = excluded.avg_duration_ms,
					p50_duration_ms = excluded.p50_duration_ms,
					p95_duration_ms = excluded.p95_duration_ms,
					p99_duration_ms = excluded.p99_duration_ms,
					max_duration_ms = excluded.max_duration_ms,
					avg_sql_count = excluded.avg_sql_count,
					avg_db_time_ms = excluded.avg_db_time_ms,
					avg_cache_hit_ratio = excluded.avg_cache_hit_ratio,
					avg_http_external_ms = excluded.avg_http_external_ms
			`, b.BucketStart.Format(time.RFC3339), b.BucketInterval, b.Service, b.Endpoint, b.Environment,
				b.RequestCount, b.ErrorCount, b.LogCount,
				b.AvgDurationMs, b.P50DurationMs, b.P95DurationMs, b.P99DurationMs, b.MaxDurationMs,
				b.AvgSQLCount, b.AvgDBTimeMs, b.AvgCacheHitRatio, b.AvgHTTPExternalMs)
			if err != nil {
				return fmt.Errorf("upserting metric bucket: %w", err)
			}
		}

		// Detect deploy markers: find distinct commit hashes in this bucket
		// Collect results first, then insert (avoids deadlock with MaxOpenConns=1)
		type deployInfo struct {
			svc, env, hash, firstSeen string
			cnt                       int
		}
		var deploys []deployInfo
		deployRows, err := s.db.QueryContext(ctx, `
			SELECT COALESCE(l.service, '') as service,
			       COALESCE(l.environment, '') as env,
			       l.commit_hash,
			       MIN(l.timestamp) as first_seen,
			       COUNT(*) as cnt
			FROM logs l
			WHERE l.timestamp >= ? AND l.timestamp < ?
			  AND l.commit_hash != ''
			GROUP BY service, env, l.commit_hash
		`, start, end)
		if err == nil {
			for deployRows.Next() {
				var d deployInfo
				if err := deployRows.Scan(&d.svc, &d.env, &d.hash, &d.firstSeen, &d.cnt); err == nil {
					deploys = append(deploys, d)
				}
			}
			deployRows.Close()
		}
		for _, d := range deploys {
			_, _ = s.db.ExecContext(ctx, `
				INSERT INTO deploy_markers (service, environment, commit_hash, first_seen_at, request_count)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(service, environment, commit_hash) DO UPDATE SET
					request_count = deploy_markers.request_count + excluded.request_count
			`, d.svc, d.env, d.hash, d.firstSeen, d.cnt)
		}
	}

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
	var conditions []string
	var args []any

	conditions = append(conditions, "first_seen_at >= ?")
	args = append(args, since.Format(time.RFC3339))

	if service != "" {
		conditions = append(conditions, "service = ?")
		args = append(args, service)
	}

	query := `SELECT id, service, environment, commit_hash, first_seen_at, request_count
		FROM deploy_markers`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY first_seen_at ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying deploy markers: %w", err)
	}
	defer rows.Close()

	var results []store.DeployMarker
	for rows.Next() {
		var d store.DeployMarker
		var firstSeenStr string
		if err := rows.Scan(&d.ID, &d.Service, &d.Environment, &d.CommitHash, &firstSeenStr, &d.RequestCount); err != nil {
			return nil, fmt.Errorf("scanning deploy marker: %w", err)
		}
		d.FirstSeenAt, _ = time.Parse(time.RFC3339, firstSeenStr)
		results = append(results, d)
	}
	return results, rows.Err()
}

// Prune removes old metric buckets and deploy markers.
func (s *trendStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var total int64

	res, err := s.db.ExecContext(ctx, `DELETE FROM metric_buckets WHERE bucket_start < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("pruning metric buckets: %w", err)
	}
	n, _ := res.RowsAffected()
	total += n

	res, err = s.db.ExecContext(ctx, `DELETE FROM deploy_markers WHERE first_seen_at < ?`, cutoff)
	if err != nil {
		return total, fmt.Errorf("pruning deploy markers: %w", err)
	}
	n, _ = res.RowsAffected()
	total += n

	return total, nil
}

// parseBucketDuration converts interval strings to time.Duration.
func parseBucketDuration(interval string) (time.Duration, error) {
	switch interval {
	case "1m":
		return time.Minute, nil
	case "5m":
		return 5 * time.Minute, nil
	case "15m":
		return 15 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	case "1d":
		return 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported interval: %s (use 1m, 5m, 15m, 1h, 1d)", interval)
	}
}

// percentile computes the p-th percentile from a sorted slice of float64.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := p / 100 * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper || upper >= len(sorted) {
		return sorted[lower]
	}
	frac := rank - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}
