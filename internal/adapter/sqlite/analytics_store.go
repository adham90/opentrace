package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type analyticsStore struct {
	db *bun.DB
}

// NewAnalyticsStore creates an AnalyticsStore backed by SQLite.
func NewAnalyticsStore(db *bun.DB) store.AnalyticsStore {
	return &analyticsStore{db: db}
}

// AggregateEndpointStats is a no-op. It previously read the `request_summaries`
// and `logs` SQLite tables; those source tables were removed when log storage
// moved to the segmented log store. Re-enabling requires porting to LogStore.
func (s *analyticsStore) AggregateEndpointStats(_ context.Context, _ string, _ time.Time) error {
	noteAggregationDisabled()
	return nil
}

// UpdateTrafficHeatmap is a no-op. It previously read `logs` + `request_summaries`
// from SQLite; those tables moved to the segmented log store. Re-enabling requires
// porting to LogStore.
func (s *analyticsStore) UpdateTrafficHeatmap(_ context.Context, _ time.Time) error {
	noteAggregationDisabled()
	return nil
}

// TopEndpoints returns endpoints ranked by the specified metric.
func (s *analyticsStore) TopEndpoints(ctx context.Context, params store.TopEndpointParams) ([]store.EndpointStat, error) {
	var conditions []string
	var args []any

	conditions = append(conditions, "period_start >= ?")
	args = append(args, params.Since.Format(time.RFC3339))
	conditions = append(conditions, "period_start <= ?")
	args = append(args, params.Until.Format(time.RFC3339))

	if params.Service != "" {
		conditions = append(conditions, "service = ?")
		args = append(args, params.Service)
	}

	minReqs := params.MinRequests
	if minReqs <= 0 {
		minReqs = 1
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}

	query := `
		SELECT service, method, controller, action, path_pattern,
		       SUM(request_count) as total_requests,
		       SUM(error_count) as total_errors,
		       SUM(client_error_count) as total_client_errors,
		       COALESCE(AVG(avg_duration_ms), 0) as avg_dur,
		       MAX(p95_duration_ms) as p95_dur,
		       MAX(max_duration_ms) as max_dur,
		       COALESCE(AVG(avg_sql_count), 0) as avg_sql,
		       SUM(status_2xx) as s2xx,
		       SUM(status_3xx) as s3xx,
		       SUM(status_4xx) as s4xx,
		       SUM(status_5xx) as s5xx
		FROM endpoint_stats
		WHERE ` + strings.Join(conditions, " AND ") + `
		GROUP BY service, method, controller, action, path_pattern
		HAVING SUM(request_count) >= ?`

	args = append(args, minReqs)

	orderBy := "total_requests DESC"
	switch params.SortBy {
	case "error_rate":
		orderBy = "CAST(total_errors AS REAL) / CAST(total_requests AS REAL) DESC"
	case "avg_duration":
		orderBy = "avg_dur DESC"
	case "p95_duration":
		orderBy = "p95_dur DESC"
	case "request_count":
		orderBy = "total_requests DESC"
	}
	query += " ORDER BY " + orderBy + " LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying top endpoints: %w", err)
	}
	defer rows.Close()

	var results []store.EndpointStat
	for rows.Next() {
		var e store.EndpointStat
		if err := rows.Scan(&e.Service, &e.Method, &e.Controller, &e.Action, &e.PathPattern,
			&e.RequestCount, &e.ErrorCount, &e.ClientErrorCount,
			&e.AvgDurationMs, &e.P95DurationMs, &e.MaxDurationMs, &e.AvgSQLCount,
			&e.Status2xx, &e.Status3xx, &e.Status4xx, &e.Status5xx); err != nil {
			return nil, fmt.Errorf("scanning endpoint stat: %w", err)
		}
		results = append(results, e)
	}
	return results, rows.Err()
}

// TrafficSummary returns a high-level overview of traffic.
func (s *analyticsStore) TrafficSummary(ctx context.Context, params store.AnalyticsParams) (*store.TrafficSummary, error) {
	sinceStr := params.Since.Format(time.RFC3339)
	untilStr := params.Until.Format(time.RFC3339)

	var conditions []string
	var args []any

	conditions = append(conditions, "l.timestamp >= ?", "l.timestamp <= ?")
	args = append(args, sinceStr, untilStr)

	if params.Service != "" {
		conditions = append(conditions, "l.service = ?")
		args = append(args, params.Service)
	}

	where := strings.Join(conditions, " AND ")

	summary := &store.TrafficSummary{
		StatusBreakdown: make(map[string]int),
		MethodBreakdown: make(map[string]int),
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) as total,
		       COUNT(DISTINCT rs.controller || '#' || rs.action) as endpoints,
		       COALESCE(AVG(rs.duration_ms), 0) as avg_dur,
		       COALESCE(SUM(CASE WHEN rs.status >= 500 THEN 1 ELSE 0 END), 0) as errors
		FROM request_summaries rs
		JOIN logs l ON rs.log_id = l.id
		WHERE `+where, args...)

	var totalReqs, endpoints, errors int
	var avgDur float64
	if err := row.Scan(&totalReqs, &endpoints, &avgDur, &errors); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("querying traffic summary: %w", err)
	}
	summary.TotalRequests = totalReqs
	summary.UniqueEndpoints = endpoints
	summary.AvgDurationMs = avgDur
	if totalReqs > 0 {
		summary.ErrorRate = float64(errors) / float64(totalReqs)
	}

	p95Row := s.db.QueryRowContext(ctx, `
		SELECT rs.duration_ms
		FROM request_summaries rs
		JOIN logs l ON rs.log_id = l.id
		WHERE `+where+`
		ORDER BY rs.duration_ms ASC
		LIMIT 1 OFFSET CAST(? * 0.95 AS INTEGER)
	`, append(args, totalReqs)...)
	_ = p95Row.Scan(&summary.P95DurationMs)

	statusRows, err := s.db.QueryContext(ctx, `
		SELECT CASE
			WHEN rs.status >= 200 AND rs.status < 300 THEN '2xx'
			WHEN rs.status >= 300 AND rs.status < 400 THEN '3xx'
			WHEN rs.status >= 400 AND rs.status < 500 THEN '4xx'
			WHEN rs.status >= 500 THEN '5xx'
			ELSE 'other'
		END as bucket,
		COUNT(*) as cnt
		FROM request_summaries rs
		JOIN logs l ON rs.log_id = l.id
		WHERE `+where+`
		GROUP BY bucket
	`, args...)
	if err == nil {
		for statusRows.Next() {
			var bucket string
			var cnt int
			if err := statusRows.Scan(&bucket, &cnt); err == nil {
				summary.StatusBreakdown[bucket] = cnt
			}
		}
		statusRows.Close()
	}

	methodRows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(rs.method, 'UNKNOWN') as method, COUNT(*) as cnt
		FROM request_summaries rs
		JOIN logs l ON rs.log_id = l.id
		WHERE `+where+`
		GROUP BY method
	`, args...)
	if err == nil {
		for methodRows.Next() {
			var method string
			var cnt int
			if err := methodRows.Scan(&method, &cnt); err == nil {
				summary.MethodBreakdown[method] = cnt
			}
		}
		methodRows.Close()
	}

	return summary, nil
}

// TrafficHeatmap returns the 24x7 traffic heatmap.
func (s *analyticsStore) TrafficHeatmap(ctx context.Context, service string) ([]store.HeatmapCell, error) {
	query := `SELECT service, day_of_week, hour_of_day, request_count, error_count, avg_duration_ms
		FROM traffic_heatmap`

	var args []any
	if service != "" {
		query += " WHERE service = ?"
		args = append(args, service)
	}
	query += " ORDER BY day_of_week, hour_of_day"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying heatmap: %w", err)
	}
	defer rows.Close()

	var results []store.HeatmapCell
	for rows.Next() {
		var c store.HeatmapCell
		if err := rows.Scan(&c.Service, &c.DayOfWeek, &c.HourOfDay, &c.RequestCount, &c.ErrorCount, &c.AvgDurationMs); err != nil {
			return nil, fmt.Errorf("scanning heatmap cell: %w", err)
		}
		results = append(results, c)
	}
	return results, rows.Err()
}

// Prune removes old endpoint stats.
func (s *analyticsStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	res, err := s.db.NewRaw(`DELETE FROM endpoint_stats WHERE period_start < ?`, cutoff).Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
