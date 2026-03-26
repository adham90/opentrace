package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/pkg/store"
)

type healthCheckStore struct {
	db *sql.DB
}

// NewHealthCheckStore creates a new HealthCheckStore backed by SQLite.
func NewHealthCheckStore(db *sql.DB) store.HealthCheckStore {
	return &healthCheckStore{db: db}
}

func (s *healthCheckStore) Create(ctx context.Context, params store.CreateHealthCheckParams) (*store.HealthCheck, error) {
	id := uuid.New().String()

	method := params.Method
	if method == "" {
		method = "GET"
	}
	interval := params.IntervalSecs
	if interval <= 0 {
		interval = 60
	}
	timeout := params.TimeoutSecs
	if timeout <= 0 {
		timeout = 10
	}
	expected := params.ExpectedStatus
	if expected <= 0 {
		expected = 200
	}

	now := time.Now().UTC().Format(time.RFC3339)
	retries := params.Retries
	if retries < 0 {
		retries = 0
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO healthchecks (id, name, url, method, interval_secs, timeout_secs, expected_status, expected_body, retries, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		id, params.Name, params.URL, method, interval, timeout, expected, params.ExpectedBody, retries, now,
	)
	if err != nil {
		return nil, err
	}

	return s.Get(ctx, id)
}

func (s *healthCheckStore) Get(ctx context.Context, id string) (*store.HealthCheck, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, url, method, interval_secs, timeout_secs, expected_status, expected_body, retries, enabled, created_at
		 FROM healthchecks WHERE id = ?`, id)

	var hc store.HealthCheck
	var enabledInt int
	var createdStr string
	err := row.Scan(&hc.ID, &hc.Name, &hc.URL, &hc.Method,
		&hc.IntervalSecs, &hc.TimeoutSecs, &hc.ExpectedStatus,
		&hc.ExpectedBody, &hc.Retries,
		&enabledInt, &createdStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	hc.Enabled = enabledInt != 0
	hc.CreatedAt = parseTime(createdStr)
	return &hc, nil
}

func (s *healthCheckStore) List(ctx context.Context, params store.ListHealthCheckParams) ([]store.HealthCheck, error) {
	query := `SELECT id, name, url, method, interval_secs, timeout_secs, expected_status, expected_body, retries, enabled, created_at
		 FROM healthchecks ORDER BY created_at DESC`
	var args []any
	if params.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, params.Limit)
		if params.Offset > 0 {
			query += ` OFFSET ?`
			args = append(args, params.Offset)
		}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var checks []store.HealthCheck
	for rows.Next() {
		var hc store.HealthCheck
		var enabledInt int
		var createdStr string
		if err := rows.Scan(&hc.ID, &hc.Name, &hc.URL, &hc.Method,
			&hc.IntervalSecs, &hc.TimeoutSecs, &hc.ExpectedStatus,
			&hc.ExpectedBody, &hc.Retries,
			&enabledInt, &createdStr); err != nil {
			return nil, err
		}
		hc.Enabled = enabledInt != 0
		hc.CreatedAt = parseTime(createdStr)
		checks = append(checks, hc)
	}
	return checks, rows.Err()
}

func (s *healthCheckStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM healthchecks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *healthCheckStore) SetEnabled(ctx context.Context, id string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE healthchecks SET enabled = ? WHERE id = ?`, v, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *healthCheckStore) RecordResult(ctx context.Context, result store.HealthCheckResult) error {
	checkedAt := result.CheckedAt
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO healthcheck_results (healthcheck_id, status, status_code, response_ms, error, checked_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		result.HealthCheckID, string(result.Status), result.StatusCode,
		result.ResponseMs, result.Error, checkedAt.Format(time.RFC3339),
	)
	return err
}

func (s *healthCheckStore) LatestResults(ctx context.Context, healthcheckID string, limit int) ([]store.HealthCheckResult, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, healthcheck_id, status, status_code, response_ms, error, checked_at
		 FROM healthcheck_results
		 WHERE healthcheck_id = ?
		 ORDER BY checked_at DESC
		 LIMIT ?`, healthcheckID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []store.HealthCheckResult
	for rows.Next() {
		var r store.HealthCheckResult
		var statusStr, checkedStr string
		if err := rows.Scan(&r.ID, &r.HealthCheckID, &statusStr, &r.StatusCode,
			&r.ResponseMs, &r.Error, &checkedStr); err != nil {
			return nil, err
		}
		r.Status = store.HealthCheckStatus(statusStr)
		r.CheckedAt = parseTime(checkedStr)
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *healthCheckStore) UptimeSummaries(ctx context.Context, since time.Time) ([]store.UptimeSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT
			h.id,
			h.name,
			h.url,
			COALESCE((SELECT status FROM healthcheck_results WHERE healthcheck_id = h.id ORDER BY checked_at DESC LIMIT 1), 'unknown') AS current_status,
			COUNT(r.id) AS total_checks,
			SUM(CASE WHEN r.status = 'down' THEN 1 ELSE 0 END) AS down_checks,
			COALESCE(AVG(r.response_ms), 0) AS avg_response_ms
		 FROM healthchecks h
		 LEFT JOIN healthcheck_results r ON r.healthcheck_id = h.id AND r.checked_at >= ?
		 GROUP BY h.id
		 ORDER BY h.name`, since.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []store.UptimeSummary
	for rows.Next() {
		var us store.UptimeSummary
		if err := rows.Scan(&us.HealthCheckID, &us.Name, &us.URL,
			&us.CurrentStatus, &us.TotalChecks, &us.DownChecks, &us.AvgResponseMs); err != nil {
			return nil, err
		}
		if us.TotalChecks > 0 {
			us.UptimePct = float64(us.TotalChecks-us.DownChecks) / float64(us.TotalChecks) * 100.0
		}
		summaries = append(summaries, us)
	}
	return summaries, rows.Err()
}

func (s *healthCheckStore) PruneResults(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `DELETE FROM healthcheck_results WHERE checked_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
