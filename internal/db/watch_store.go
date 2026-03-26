package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/adham90/opentrace/pkg/store"
)

type watchStore struct {
	db *sql.DB
	q  *Queries
}

// NewWatchStore creates a new WatchStore backed by SQLite.
func NewWatchStore(db *sql.DB) store.WatchStore {
	return &watchStore{db: db, q: New(db)}
}

func (s *watchStore) Create(ctx context.Context, params store.CreateWatchParams) (*store.Watch, error) {
	// Validate threshold is a finite number
	if math.IsNaN(params.Threshold) || math.IsInf(params.Threshold, 0) {
		return nil, fmt.Errorf("threshold must be a finite number")
	}

	id := uuid.New().String()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	// Defaults
	duration := params.Duration
	if duration == "" {
		duration = "1h"
	}
	urgency := params.Urgency
	if urgency == "" {
		urgency = store.WatchUrgencyNormal
	}
	checkInterval := params.CheckInterval
	if checkInterval == "" {
		checkInterval = "30s"
	}
	baselineWindow := params.BaselineWindow
	if baselineWindow == "" {
		baselineWindow = "1h"
	}
	minConsecutive := params.MinConsecutive
	if minConsecutive <= 0 {
		minConsecutive = 1
	}

	// Calculate expires_at from duration
	dur, err := time.ParseDuration(duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration %q: %w", duration, err)
	}
	if dur <= 0 {
		return nil, fmt.Errorf("duration must be positive, got %s", duration)
	}
	expiresAt := nullString(now.Add(dur).Format(time.RFC3339))

	// Calculate next_check_at from check_interval
	var nextCheckAt sql.NullString
	ci, err := time.ParseDuration(checkInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid check_interval %q: %w", checkInterval, err)
	}
	if ci > 0 {
		nextCheckAt = nullString(now.Add(ci).Format(time.RFC3339))
	}

	err = s.q.CreateWatch(ctx, CreateWatchParams{
		ID:             id,
		Metric:         string(params.Metric),
		Operator:       string(params.Operator),
		Threshold:      params.Threshold,
		Service:        params.Service,
		Endpoint:       params.Endpoint,
		Environment:    params.Environment,
		CommitHash:     params.CommitHash,
		Duration:       duration,
		Urgency:        string(urgency),
		CheckInterval:  checkInterval,
		BaselineWindow: baselineWindow,
		MinConsecutive: int64(minConsecutive),
		Status:         string(store.WatchStatusActive),
		ExpiresAt:      expiresAt,
		CreatedBy:      params.CreatedBy,
		SessionID:      params.SessionID,
		NextCheckAt:    nextCheckAt,
		CreatedAt:      nowStr,
		UpdatedAt:      nowStr,
	})
	if err != nil {
		return nil, fmt.Errorf("inserting watch: %w", err)
	}

	return s.GetByID(ctx, id)
}

func (s *watchStore) GetByID(ctx context.Context, id string) (*store.Watch, error) {
	row, err := s.q.GetWatch(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying watch: %w", err)
	}
	return watchToStore(row), nil
}

func (s *watchStore) List(ctx context.Context, params store.ListWatchParams) ([]store.Watch, error) {
	qb := psql.Select(
		"id", "metric", "operator", "threshold", "service", "endpoint", "environment", "commit_hash",
		"duration", "urgency", "check_interval", "baseline_window", "min_consecutive", "status",
		"baseline_json", "consecutive_breaches", "current_value", "expires_at", "created_by",
		"session_id", "last_checked_at", "next_check_at", "created_at", "updated_at").
		From("watches")

	if params.Status != "" {
		qb = qb.Where(sq.Eq{"status": string(params.Status)})
	}
	if params.Service != "" {
		qb = qb.Where(sq.Eq{"service": params.Service})
	}
	if params.SessionID != "" {
		qb = qb.Where(sq.Eq{"session_id": params.SessionID})
	}

	qb = qb.OrderBy("created_at DESC")

	if params.Limit > 0 {
		qb = qb.Limit(uint64(params.Limit))
		if params.Offset > 0 {
			qb = qb.Offset(uint64(params.Offset))
		}
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building list query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying watches: %w", err)
	}
	defer rows.Close()

	result := make([]store.Watch, 0)
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning watch: %w", err)
		}
		result = append(result, *w)
	}
	return result, rows.Err()
}

func (s *watchStore) UpdateStatus(ctx context.Context, id string, status store.WatchStatus) error {
	now := time.Now().UTC().Format(time.RFC3339)
	n, err := s.q.UpdateWatchStatus(ctx, UpdateWatchStatusParams{
		Status:    string(status),
		UpdatedAt: now,
		ID:        id,
	})
	if err != nil {
		return fmt.Errorf("updating watch status: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) UpdateAfterCheck(ctx context.Context, id string, value float64, breaches int, nextCheck time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339)
	n, err := s.q.UpdateWatchAfterCheck(ctx, UpdateWatchAfterCheckParams{
		CurrentValue:        sql.NullFloat64{Float64: value, Valid: true},
		ConsecutiveBreaches: int64(breaches),
		LastCheckedAt:       sql.NullString{String: now, Valid: true},
		NextCheckAt:         sql.NullString{String: nextCheck.Format(time.RFC3339), Valid: true},
		UpdatedAt:           now,
		ID:                  id,
	})
	if err != nil {
		return fmt.Errorf("updating watch after check: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) UpdateBaseline(ctx context.Context, id string, baseline *store.WatchBaseline) error {
	now := time.Now().UTC().Format(time.RFC3339)
	var baselineStr sql.NullString
	if baseline != nil {
		b, err := json.Marshal(baseline)
		if err != nil {
			return fmt.Errorf("marshaling baseline: %w", err)
		}
		baselineStr = sql.NullString{String: string(b), Valid: true}
	}

	n, err := s.q.UpdateWatchBaseline(ctx, UpdateWatchBaselineParams{
		BaselineJson: baselineStr,
		UpdatedAt:    now,
		ID:           id,
	})
	if err != nil {
		return fmt.Errorf("updating watch baseline: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) Delete(ctx context.Context, id string) error {
	n, err := s.q.DeleteWatch(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting watch: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) GetDueWatches(ctx context.Context) ([]store.Watch, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.q.GetDueWatches(ctx, sql.NullString{String: now, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("querying due watches: %w", err)
	}
	return watchesToStore(rows), nil
}

func (s *watchStore) ExpireWatches(ctx context.Context) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	n, err := s.q.ExpireWatches(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("expiring watches: %w", err)
	}
	return int(n), nil
}

// --- Run CRUD ---

func (s *watchStore) CreateRun(ctx context.Context, watchID string) (*store.WatchRun, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	err := s.q.CreateWatchRun(ctx, CreateWatchRunParams{
		ID:        id,
		WatchID:   watchID,
		StartedAt: now,
	})
	if err != nil {
		return nil, fmt.Errorf("inserting watch run: %w", err)
	}

	return &store.WatchRun{
		ID:        id,
		WatchID:   watchID,
		Status:    "running",
		StartedAt: func() time.Time { t, _ := time.Parse(time.RFC3339, now); return t }(),
	}, nil
}

func (s *watchStore) CompleteRun(ctx context.Context, id string, value float64, breached bool, summary string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	n, err := s.q.CompleteWatchRun(ctx, CompleteWatchRunParams{
		MetricValue: sql.NullFloat64{Float64: value, Valid: true},
		Breached:    boolToInt64(breached),
		Summary:     sql.NullString{String: summary, Valid: summary != ""},
		FinishedAt:  sql.NullString{String: now, Valid: true},
		ID:          id,
	})
	if err != nil {
		return fmt.Errorf("completing watch run: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) FailRun(ctx context.Context, id string, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	n, err := s.q.FailWatchRun(ctx, FailWatchRunParams{
		ErrorMessage: sql.NullString{String: errMsg, Valid: errMsg != ""},
		FinishedAt:   sql.NullString{String: now, Valid: true},
		ID:           id,
	})
	if err != nil {
		return fmt.Errorf("failing watch run: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) ListRuns(ctx context.Context, watchID string, limit int) ([]store.WatchRun, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.q.ListWatchRuns(ctx, ListWatchRunsParams{
		WatchID:  watchID,
		RowLimit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("querying watch runs: %w", err)
	}
	return watchRunsToStore(rows), nil
}

// --- Alert CRUD ---

func (s *watchStore) CreateAlert(ctx context.Context, params store.CreateWatchAlertParams) (*store.WatchAlert, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	var evidenceStr sql.NullString
	if params.Evidence != nil {
		b, err := json.Marshal(params.Evidence)
		if err != nil {
			return nil, fmt.Errorf("marshaling evidence: %w", err)
		}
		evidenceStr = sql.NullString{String: string(b), Valid: true}
	}

	err := s.q.CreateWatchAlert(ctx, CreateWatchAlertParams{
		ID:             id,
		WatchID:        params.WatchID,
		RunID:          nullString(params.RunID),
		Urgency:        string(params.Urgency),
		Summary:        params.Summary,
		TriggerMetric:  params.TriggerMetric,
		TriggerValue:   params.TriggerValue,
		ThresholdValue: params.ThresholdValue,
		EvidenceJson:   evidenceStr,
		CreatedAt:      now,
	})
	if err != nil {
		return nil, fmt.Errorf("inserting watch alert: %w", err)
	}

	return s.GetAlert(ctx, id)
}

func (s *watchStore) GetAlert(ctx context.Context, id string) (*store.WatchAlert, error) {
	row, err := s.q.GetWatchAlert(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying watch alert: %w", err)
	}
	return watchAlertToStore(row), nil
}

func (s *watchStore) ListAlerts(ctx context.Context, watchID string, status string, limit int) ([]store.WatchAlert, error) {
	if limit <= 0 {
		limit = 20
	}

	qb := psql.Select(
		"id", "watch_id", "run_id", "urgency", "summary", "trigger_metric", "trigger_value",
		"threshold_value", "evidence_json", "status", "dismiss_reason", "created_at").
		From("watch_alerts")

	if watchID != "" {
		qb = qb.Where(sq.Eq{"watch_id": watchID})
	}
	if status != "" {
		qb = qb.Where(sq.Eq{"status": status})
	}
	qb = qb.OrderBy("created_at DESC").Limit(uint64(limit))

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building list alerts query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying watch alerts: %w", err)
	}
	defer rows.Close()

	result := make([]store.WatchAlert, 0)
	for rows.Next() {
		a, err := scanWatchAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning watch alert: %w", err)
		}
		result = append(result, *a)
	}
	return result, rows.Err()
}

func (s *watchStore) DismissAlert(ctx context.Context, id string, reason string) error {
	n, err := s.q.DismissWatchAlert(ctx, DismissWatchAlertParams{
		DismissReason: sql.NullString{String: reason, Valid: reason != ""},
		ID:            id,
	})
	if err != nil {
		return fmt.Errorf("dismissing watch alert: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) AcknowledgeAlert(ctx context.Context, id string) error {
	n, err := s.q.AcknowledgeWatchAlert(ctx, id)
	if err != nil {
		return fmt.Errorf("acknowledging watch alert: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) CountPendingAlerts(ctx context.Context) (int, error) {
	n, err := s.q.CountPendingAlerts(ctx)
	if err != nil {
		return 0, fmt.Errorf("counting pending watch alerts: %w", err)
	}
	return int(n), nil
}

// scanWatch and scanWatchAlert kept for squirrel dynamic queries
func scanWatch(sc interface{ Scan(...any) error }) (*store.Watch, error) {
	w := &store.Watch{}
	var baselineStr sql.NullString
	var currentVal sql.NullFloat64
	var expiresAt, lastChecked, nextCheck sql.NullString
	var createdAt, updatedAt string

	err := sc.Scan(
		&w.ID, &w.Metric, &w.Operator, &w.Threshold,
		&w.Service, &w.Endpoint, &w.Environment, &w.CommitHash,
		&w.Duration, &w.Urgency, &w.CheckInterval, &w.BaselineWindow,
		&w.MinConsecutive, &w.Status,
		&baselineStr, &w.ConsecutiveBreaches, &currentVal,
		&expiresAt, &w.CreatedBy, &w.SessionID,
		&lastChecked, &nextCheck, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	if baselineStr.Valid {
		var b store.WatchBaseline
		if err := json.Unmarshal([]byte(baselineStr.String), &b); err == nil {
			w.BaselineJSON = &b
		}
	}
	if currentVal.Valid {
		w.CurrentValue = &currentVal.Float64
	}
	if expiresAt.Valid {
		t := parseTime(expiresAt.String)
		w.ExpiresAt = &t
	}
	if lastChecked.Valid {
		t := parseTime(lastChecked.String)
		w.LastCheckedAt = &t
	}
	if nextCheck.Valid {
		t := parseTime(nextCheck.String)
		w.NextCheckAt = &t
	}
	w.CreatedAt = parseTime(createdAt)
	w.UpdatedAt = parseTime(updatedAt)

	return w, nil
}

func scanWatchAlert(sc interface{ Scan(...any) error }) (*store.WatchAlert, error) {
	a := &store.WatchAlert{}
	var runID sql.NullString
	var evidenceStr sql.NullString
	var dismissReason sql.NullString
	var createdAt string

	err := sc.Scan(&a.ID, &a.WatchID, &runID, &a.Urgency, &a.Summary,
		&a.TriggerMetric, &a.TriggerValue, &a.ThresholdValue,
		&evidenceStr, &a.Status, &dismissReason, &createdAt,
	)
	if err != nil {
		return nil, err
	}

	if runID.Valid {
		a.RunID = runID.String
	}
	if evidenceStr.Valid {
		var ev store.WatchEvidenceBundle
		if err := json.Unmarshal([]byte(evidenceStr.String), &ev); err == nil {
			a.EvidenceJSON = &ev
		}
	}
	if dismissReason.Valid {
		a.DismissReason = dismissReason.String
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return a, nil
}
