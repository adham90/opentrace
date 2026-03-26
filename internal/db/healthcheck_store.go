package db

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
	q  *Queries
}

// NewHealthCheckStore creates a new HealthCheckStore backed by SQLite.
func NewHealthCheckStore(db *sql.DB) store.HealthCheckStore {
	return &healthCheckStore{db: db, q: New(db)}
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
	err := s.q.CreateHealthcheck(ctx, CreateHealthcheckParams{
		ID:             id,
		Name:           params.Name,
		Url:            params.URL,
		Method:         method,
		IntervalSecs:   int64(interval),
		TimeoutSecs:    int64(timeout),
		ExpectedStatus: int64(expected),
		ExpectedBody:   params.ExpectedBody,
		Retries:        int64(retries),
		CreatedAt:      now,
	})
	if err != nil {
		return nil, err
	}

	return s.Get(ctx, id)
}

func (s *healthCheckStore) Get(ctx context.Context, id string) (*store.HealthCheck, error) {
	row, err := s.q.GetHealthcheck(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return healthcheckRowToStore(row), nil
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

	rows, err := s.q.ListHealthchecks(ctx, ListHealthchecksParams{
		RowOffset: int64(offset),
		RowLimit:  int64(limit),
	})
	if err != nil {
		return nil, err
	}

	checks := make([]store.HealthCheck, len(rows))
	for i, row := range rows {
		checks[i] = healthcheckListRowToStore(row)
	}
	return checks, nil
}

func (s *healthCheckStore) Delete(ctx context.Context, id string) error {
	n, err := s.q.DeleteHealthcheck(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *healthCheckStore) SetEnabled(ctx context.Context, id string, enabled bool) error {
	v := int64(0)
	if enabled {
		v = 1
	}
	n, err := s.q.SetHealthcheckEnabled(ctx, SetHealthcheckEnabledParams{
		Enabled: v,
		ID:      id,
	})
	if err != nil {
		return err
	}
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

	return s.q.InsertHealthcheckResult(ctx, InsertHealthcheckResultParams{
		HealthcheckID: result.HealthCheckID,
		Status:        string(result.Status),
		StatusCode:    statusCode,
		ResponseMs:    responseMs,
		Error:         result.Error,
		CheckedAt:     checkedAt.Format(time.RFC3339),
	})
}

func (s *healthCheckStore) LatestResults(ctx context.Context, healthcheckID string, limit int) ([]store.HealthCheckResult, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.q.ListLatestHealthcheckResults(ctx, ListLatestHealthcheckResultsParams{
		HealthcheckID: healthcheckID,
		RowLimit:      int64(limit),
	})
	if err != nil {
		return nil, err
	}
	return healthcheckResultsToStore(rows), nil
}

func (s *healthCheckStore) UptimeSummaries(ctx context.Context, since time.Time) ([]store.UptimeSummary, error) {
	rows, err := s.q.UptimeSummaries(ctx, since.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}

	var summaries []store.UptimeSummary
	for _, row := range rows {
		var us store.UptimeSummary
		us.HealthCheckID = row.ID
		us.Name = row.Name
		us.URL = row.Url

		// CurrentStatus comes as interface{} from sqlc
		if cs, ok := row.CurrentStatus.(string); ok {
			us.CurrentStatus = cs
		} else if cs, ok := row.CurrentStatus.([]byte); ok {
			us.CurrentStatus = string(cs)
		} else {
			us.CurrentStatus = "unknown"
		}

		us.TotalChecks = int(row.TotalChecks)
		if row.DownChecks.Valid {
			us.DownChecks = int(row.DownChecks.Float64)
		}

		// AvgResponseMs comes as interface{} from sqlc
		switch v := row.AvgResponseMs.(type) {
		case float64:
			us.AvgResponseMs = v
		case int64:
			us.AvgResponseMs = float64(v)
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
	return s.q.PruneHealthcheckResults(ctx, cutoff)
}
