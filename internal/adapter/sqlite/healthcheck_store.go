package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type healthCheckStore struct {
	db *bun.DB
}

// NewHealthCheckStore creates a new HealthCheckStore backed by SQLite.
func NewHealthCheckStore(db *bun.DB) store.HealthCheckStore {
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
	retries := params.Retries
	if retries < 0 {
		retries = 0
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.NewRaw(`
		INSERT INTO healthchecks (id, name, url, method, interval_secs, timeout_secs,
			expected_status, expected_body, retries, environment, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, params.Name, params.URL, method, interval, timeout,
		expected, params.ExpectedBody, retries, params.Environment, now,
	).Exec(ctx)
	if err != nil {
		return nil, err
	}

	return s.Get(ctx, id)
}

func (s *healthCheckStore) Get(ctx context.Context, id string) (*store.HealthCheck, error) {
	var hc struct {
		ID             string `bun:"id"`
		Name           string `bun:"name"`
		URL            string `bun:"url"`
		Method         string `bun:"method"`
		IntervalSecs   int    `bun:"interval_secs"`
		TimeoutSecs    int    `bun:"timeout_secs"`
		ExpectedStatus int    `bun:"expected_status"`
		ExpectedBody   string `bun:"expected_body"`
		Retries        int    `bun:"retries"`
		Enabled        int    `bun:"enabled"`
		Environment    string `bun:"environment"`
		CreatedAt      string `bun:"created_at"`
	}

	err := s.db.NewRaw(`
		SELECT id, name, url, method, interval_secs, timeout_secs,
			expected_status, expected_body, retries, enabled, environment, created_at
		FROM healthchecks WHERE id = ?`, id,
	).Scan(ctx, &hc)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, store.ErrNotFound
		}
		return nil, err
	}

	return &store.HealthCheck{
		ID:             hc.ID,
		Name:           hc.Name,
		URL:            hc.URL,
		Method:         hc.Method,
		IntervalSecs:   hc.IntervalSecs,
		TimeoutSecs:    hc.TimeoutSecs,
		ExpectedStatus: hc.ExpectedStatus,
		ExpectedBody:   hc.ExpectedBody,
		Retries:        hc.Retries,
		Enabled:        hc.Enabled == 1,
		Environment:    hc.Environment,
		CreatedAt:      parseTime(hc.CreatedAt),
	}, nil
}

func (s *healthCheckStore) List(ctx context.Context, params store.ListHealthCheckParams) ([]store.HealthCheck, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}

	type row struct {
		ID             string `bun:"id"`
		Name           string `bun:"name"`
		URL            string `bun:"url"`
		Method         string `bun:"method"`
		IntervalSecs   int    `bun:"interval_secs"`
		TimeoutSecs    int    `bun:"timeout_secs"`
		ExpectedStatus int    `bun:"expected_status"`
		ExpectedBody   string `bun:"expected_body"`
		Retries        int    `bun:"retries"`
		Enabled        int    `bun:"enabled"`
		Environment    string `bun:"environment"`
		CreatedAt      string `bun:"created_at"`
	}

	sqlStr := `SELECT id, name, url, method, interval_secs, timeout_secs,
			expected_status, expected_body, retries, enabled, environment, created_at
		FROM healthchecks`
	args := []any{}
	if params.Environment != "" {
		sqlStr += ` WHERE environment = ?`
		args = append(args, params.Environment)
	}
	sqlStr += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	var rows []row
	err := s.db.NewRaw(sqlStr, args...).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	checks := make([]store.HealthCheck, len(rows))
	for i, r := range rows {
		checks[i] = store.HealthCheck{
			ID:             r.ID,
			Name:           r.Name,
			URL:            r.URL,
			Method:         r.Method,
			IntervalSecs:   r.IntervalSecs,
			TimeoutSecs:    r.TimeoutSecs,
			ExpectedStatus: r.ExpectedStatus,
			ExpectedBody:   r.ExpectedBody,
			Retries:        r.Retries,
			Enabled:        r.Enabled == 1,
			Environment:    r.Environment,
			CreatedAt:      parseTime(r.CreatedAt),
		}
	}
	return checks, nil
}

func (s *healthCheckStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.NewRaw(`DELETE FROM healthchecks WHERE id = ?`, id).Exec(ctx)
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
	res, err := s.db.NewRaw(`UPDATE healthchecks SET enabled = ? WHERE id = ?`, v, id).Exec(ctx)
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

	var statusCode sql.NullInt64
	if result.StatusCode != nil {
		statusCode = sql.NullInt64{Int64: int64(*result.StatusCode), Valid: true}
	}
	var responseMs sql.NullInt64
	if result.ResponseMs != nil {
		responseMs = sql.NullInt64{Int64: int64(*result.ResponseMs), Valid: true}
	}

	_, err := s.db.NewRaw(`
		INSERT INTO healthcheck_results (healthcheck_id, status, status_code, response_ms, error, checked_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		result.HealthCheckID, string(result.Status), statusCode, responseMs,
		result.Error, checkedAt.Format(time.RFC3339),
	).Exec(ctx)
	return err
}

func (s *healthCheckStore) LatestResults(ctx context.Context, healthcheckID string, limit int) ([]store.HealthCheckResult, error) {
	if limit <= 0 {
		limit = 20
	}

	type row struct {
		HealthcheckID string        `bun:"healthcheck_id"`
		Status        string        `bun:"status"`
		StatusCode    sql.NullInt64 `bun:"status_code"`
		ResponseMs    sql.NullInt64 `bun:"response_ms"`
		Error         string        `bun:"error"`
		CheckedAt     string        `bun:"checked_at"`
	}

	var rows []row
	err := s.db.NewRaw(`
		SELECT healthcheck_id, status, status_code, response_ms, error, checked_at
		FROM healthcheck_results
		WHERE healthcheck_id = ?
		ORDER BY checked_at DESC
		LIMIT ?`, healthcheckID, limit,
	).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	results := make([]store.HealthCheckResult, len(rows))
	for i, r := range rows {
		results[i] = store.HealthCheckResult{
			HealthCheckID: r.HealthcheckID,
			Status:        store.HealthCheckStatus(r.Status),
			Error:         r.Error,
			CheckedAt:     parseTime(r.CheckedAt),
		}
		if r.StatusCode.Valid {
			v := int(r.StatusCode.Int64)
			results[i].StatusCode = &v
		}
		if r.ResponseMs.Valid {
			v := int(r.ResponseMs.Int64)
			results[i].ResponseMs = &v
		}
	}
	return results, nil
}

func (s *healthCheckStore) UptimeSummaries(ctx context.Context, since time.Time) ([]store.UptimeSummary, error) {
	type row struct {
		ID            string         `bun:"id"`
		Name          string         `bun:"name"`
		URL           string         `bun:"url"`
		CurrentStatus string         `bun:"current_status"`
		TotalChecks   int            `bun:"total_checks"`
		DownChecks    sql.NullInt64  `bun:"down_checks"`
		AvgResponseMs sql.NullFloat64 `bun:"avg_response_ms"`
	}

	var rows []row
	err := s.db.NewRaw(`
		SELECT h.id, h.name, h.url,
			COALESCE((SELECT r.status FROM healthcheck_results r
				WHERE r.healthcheck_id = h.id ORDER BY r.checked_at DESC LIMIT 1), 'unknown') AS current_status,
			COUNT(hr.rowid) AS total_checks,
			SUM(CASE WHEN hr.status = 'down' THEN 1 ELSE 0 END) AS down_checks,
			AVG(hr.response_ms) AS avg_response_ms
		FROM healthchecks h
		LEFT JOIN healthcheck_results hr ON hr.healthcheck_id = h.id AND hr.checked_at >= ?
		GROUP BY h.id`, since.Format(time.RFC3339),
	).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	var summaries []store.UptimeSummary
	for _, r := range rows {
		us := store.UptimeSummary{
			HealthCheckID: r.ID,
			Name:          r.Name,
			URL:           r.URL,
			CurrentStatus: r.CurrentStatus,
			TotalChecks:   r.TotalChecks,
		}
		if r.DownChecks.Valid {
			us.DownChecks = int(r.DownChecks.Int64)
		}
		if r.AvgResponseMs.Valid {
			us.AvgResponseMs = r.AvgResponseMs.Float64
		}
		if us.TotalChecks > 0 {
			us.UptimePct = float64(us.TotalChecks-us.DownChecks) / float64(us.TotalChecks) * 100.0
		}
		summaries = append(summaries, us)
	}
	return summaries, nil
}

func (s *healthCheckStore) PruneResults(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	res, err := s.db.NewRaw(`DELETE FROM healthcheck_results WHERE checked_at < ?`, cutoff).Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
