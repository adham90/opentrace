package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type analyticsStore struct {
	db *sql.DB
}

// NewAnalyticsStore creates an AnalyticsStore backed by SQLite.
func NewAnalyticsStore(db *sql.DB) store.AnalyticsStore {
	return &analyticsStore{db: db}
}

// AggregateEndpointStats reads request_summaries and populates endpoint_stats.
func (s *analyticsStore) AggregateEndpointStats(ctx context.Context, period string, since time.Time) error {
	periodDur, err := parseBucketDuration(period)
	if err != nil {
		return fmt.Errorf("invalid period %q: %w", period, err)
	}

	now := time.Now().UTC()
	since = since.Truncate(periodDur)

	for t := since; t.Before(now); t = t.Add(periodDur) {
		periodEnd := t.Add(periodDur)
		if periodEnd.After(now) {
			periodEnd = now
		}

		start := t.Format(time.RFC3339)
		end := periodEnd.Format(time.RFC3339)
		periodStart := t.Format(time.RFC3339)

		rows, err := s.db.QueryContext(ctx, `
			SELECT COALESCE(l.service, '') as service,
			       COALESCE(rs.method, '') as method,
			       COALESCE(rs.controller, '') as controller,
			       COALESCE(rs.action, '') as action,
			       COALESCE(rs.path, '') as path_pattern,
			       COUNT(*) as request_count,
			       SUM(CASE WHEN rs.status >= 500 THEN 1 ELSE 0 END) as error_count,
			       SUM(CASE WHEN rs.status >= 400 AND rs.status < 500 THEN 1 ELSE 0 END) as client_error_count,
			       AVG(rs.duration_ms) as avg_duration,
			       MAX(rs.duration_ms) as max_duration,
			       AVG(rs.sql_count) as avg_sql,
			       SUM(CASE WHEN rs.status >= 200 AND rs.status < 300 THEN 1 ELSE 0 END) as s2xx,
			       SUM(CASE WHEN rs.status >= 300 AND rs.status < 400 THEN 1 ELSE 0 END) as s3xx,
			       SUM(CASE WHEN rs.status >= 400 AND rs.status < 500 THEN 1 ELSE 0 END) as s4xx,
			       SUM(CASE WHEN rs.status >= 500 THEN 1 ELSE 0 END) as s5xx
			FROM request_summaries rs
			JOIN logs l ON rs.log_id = l.id
			WHERE l.timestamp >= ? AND l.timestamp < ?
			GROUP BY service, method, controller, action, path_pattern
		`, start, end)
		if err != nil {
			return fmt.Errorf("querying endpoint stats: %w", err)
		}

		type row struct {
			service, method, controller, action, path string
			requestCount, errorCount, clientErrorCount int
			avgDur, maxDur, avgSQL                     float64
			s2xx, s3xx, s4xx, s5xx                     int
		}
		var aggRows []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.service, &r.method, &r.controller, &r.action, &r.path,
				&r.requestCount, &r.errorCount, &r.clientErrorCount,
				&r.avgDur, &r.maxDur, &r.avgSQL,
				&r.s2xx, &r.s3xx, &r.s4xx, &r.s5xx); err != nil {
				rows.Close()
				return fmt.Errorf("scanning endpoint stat: %w", err)
			}
			aggRows = append(aggRows, r)
		}
		rows.Close()

		// Compute p95 per endpoint
		for _, r := range aggRows {
			// Get p95 for this specific endpoint
			var p95 float64
			p95Row := s.db.QueryRowContext(ctx, `
				SELECT rs.duration_ms
				FROM request_summaries rs
				JOIN logs l ON rs.log_id = l.id
				WHERE l.timestamp >= ? AND l.timestamp < ?
				  AND COALESCE(l.service, '') = ?
				  AND COALESCE(rs.controller, '') = ?
				  AND COALESCE(rs.action, '') = ?
				ORDER BY rs.duration_ms ASC
				LIMIT 1 OFFSET CAST(? * 0.95 AS INTEGER)
			`, start, end, r.service, r.controller, r.action, r.requestCount)
			if err := p95Row.Scan(&p95); err != nil {
				p95 = r.avgDur // fallback
			}

			_, err := s.db.ExecContext(ctx, `
				INSERT INTO endpoint_stats (
					period, period_start, service, method, controller, action, path_pattern,
					request_count, error_count, client_error_count,
					avg_duration_ms, p95_duration_ms, max_duration_ms, avg_sql_count,
					status_2xx, status_3xx, status_4xx, status_5xx
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(period, period_start, service, method, controller, action) DO UPDATE SET
					request_count = excluded.request_count,
					error_count = excluded.error_count,
					client_error_count = excluded.client_error_count,
					avg_duration_ms = excluded.avg_duration_ms,
					p95_duration_ms = excluded.p95_duration_ms,
					max_duration_ms = excluded.max_duration_ms,
					avg_sql_count = excluded.avg_sql_count,
					status_2xx = excluded.status_2xx,
					status_3xx = excluded.status_3xx,
					status_4xx = excluded.status_4xx,
					status_5xx = excluded.status_5xx
			`, period, periodStart, r.service, r.method, r.controller, r.action, r.path,
				r.requestCount, r.errorCount, r.clientErrorCount,
				r.avgDur, p95, r.maxDur, r.avgSQL,
				r.s2xx, r.s3xx, r.s4xx, r.s5xx)
			if err != nil {
				return fmt.Errorf("upserting endpoint stat: %w", err)
			}
		}
	}

	return nil
}

// UpdateTrafficHeatmap recalculates the 24x7 heatmap from request_summaries.
func (s *analyticsStore) UpdateTrafficHeatmap(ctx context.Context, since time.Time) error {
	sinceStr := since.Format(time.RFC3339)

	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(l.service, '') as service,
		       CAST(strftime('%w', l.timestamp) AS INTEGER) as dow,
		       CAST(strftime('%H', l.timestamp) AS INTEGER) as hod,
		       COUNT(*) as cnt,
		       SUM(CASE WHEN rs.status >= 500 THEN 1 ELSE 0 END) as errs,
		       AVG(rs.duration_ms) as avg_dur
		FROM request_summaries rs
		JOIN logs l ON rs.log_id = l.id
		WHERE l.timestamp >= ?
		GROUP BY service, dow, hod
	`, sinceStr)
	if err != nil {
		return fmt.Errorf("querying heatmap data: %w", err)
	}

	// Collect all rows first to avoid holding connection during upserts
	type heatmapRow struct {
		service string
		dow     int
		hod     int
		cnt     int
		errs    int
		avgDur  float64
	}
	var heatmapRows []heatmapRow
	for rows.Next() {
		var h heatmapRow
		if err := rows.Scan(&h.service, &h.dow, &h.hod, &h.cnt, &h.errs, &h.avgDur); err != nil {
			rows.Close()
			return fmt.Errorf("scanning heatmap: %w", err)
		}
		heatmapRows = append(heatmapRows, h)
	}
	rows.Close()

	for _, h := range heatmapRows {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO traffic_heatmap (service, day_of_week, hour_of_day, request_count, error_count, avg_duration_ms, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(service, day_of_week, hour_of_day) DO UPDATE SET
				request_count = excluded.request_count,
				error_count = excluded.error_count,
				avg_duration_ms = excluded.avg_duration_ms,
				updated_at = excluded.updated_at
		`, h.service, h.dow, h.hod, h.cnt, h.errs, h.avgDur, time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			return fmt.Errorf("upserting heatmap cell: %w", err)
		}
	}
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

	// Aggregate across periods to get overall stats per endpoint
	query := `
		SELECT service, method, controller, action, path_pattern,
		       SUM(request_count) as total_requests,
		       SUM(error_count) as total_errors,
		       SUM(client_error_count) as total_client_errors,
		       AVG(avg_duration_ms) as avg_dur,
		       MAX(p95_duration_ms) as p95_dur,
		       MAX(max_duration_ms) as max_dur,
		       AVG(avg_sql_count) as avg_sql,
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

	// Overall counts
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) as total,
		       COUNT(DISTINCT rs.controller || '#' || rs.action) as endpoints,
		       AVG(rs.duration_ms) as avg_dur,
		       SUM(CASE WHEN rs.status >= 500 THEN 1 ELSE 0 END) as errors
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

	// P95 overall
	p95Row := s.db.QueryRowContext(ctx, `
		SELECT rs.duration_ms
		FROM request_summaries rs
		JOIN logs l ON rs.log_id = l.id
		WHERE `+where+`
		ORDER BY rs.duration_ms ASC
		LIMIT 1 OFFSET CAST(? * 0.95 AS INTEGER)
	`, append(args, totalReqs)...)
	_ = p95Row.Scan(&summary.P95DurationMs)

	// Status breakdown
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

	// Method breakdown
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
	res, err := s.db.ExecContext(ctx, `DELETE FROM endpoint_stats WHERE period_start < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("pruning endpoint stats: %w", err)
	}
	return res.RowsAffected()
}
