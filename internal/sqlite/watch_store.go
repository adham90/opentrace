package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/pkg/store"
)

type watchStore struct {
	db *sql.DB
}

// NewWatchStore creates a new WatchStore backed by SQLite.
func NewWatchStore(db *sql.DB) store.WatchStore {
	return &watchStore{db: db}
}

const watchColumns = `id, metric, operator, threshold, service, endpoint, environment, commit_hash,
	duration, urgency, check_interval, baseline_window, min_consecutive, status,
	baseline_json, consecutive_breaches, current_value, expires_at, created_by,
	session_id, last_checked_at, next_check_at, created_at, updated_at`

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
	var expiresAtStr *string
	dur, err := time.ParseDuration(duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration %q: %w", duration, err)
	}
	if dur <= 0 {
		return nil, fmt.Errorf("duration must be positive, got %s", duration)
	}
	ea := now.Add(dur).Format(time.RFC3339)
	expiresAtStr = &ea

	// Calculate next_check_at from check_interval
	var nextCheckStr *string
	ci, err := time.ParseDuration(checkInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid check_interval %q: %w", checkInterval, err)
	}
	if ci > 0 {
		nc := now.Add(ci).Format(time.RFC3339)
		nextCheckStr = &nc
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO watches (id, metric, operator, threshold, service, endpoint, environment, commit_hash,
			duration, urgency, check_interval, baseline_window, min_consecutive, status,
			consecutive_breaches, expires_at, created_by, session_id, next_check_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, string(params.Metric), string(params.Operator), params.Threshold,
		params.Service, params.Endpoint, params.Environment, params.CommitHash,
		duration, string(urgency), checkInterval, baselineWindow, minConsecutive,
		string(store.WatchStatusActive), 0,
		expiresAtStr, params.CreatedBy, params.SessionID, nextCheckStr, nowStr, nowStr,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting watch: %w", err)
	}

	return s.GetByID(ctx, id)
}

func (s *watchStore) GetByID(ctx context.Context, id string) (*store.Watch, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+watchColumns+` FROM watches WHERE id = ?`, id,
	)
	w, err := scanWatch(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("querying watch: %w", err)
	}
	return w, nil
}

func (s *watchStore) List(ctx context.Context, params store.ListWatchParams) ([]store.Watch, error) {
	query := `SELECT ` + watchColumns + ` FROM watches`
	var conditions []string
	var args []any

	if params.Status != "" {
		conditions = append(conditions, `status = ?`)
		args = append(args, string(params.Status))
	}
	if params.Service != "" {
		conditions = append(conditions, `service = ?`)
		args = append(args, params.Service)
	}
	if params.SessionID != "" {
		conditions = append(conditions, `session_id = ?`)
		args = append(args, params.SessionID)
	}

	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY created_at DESC`

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
	res, err := s.db.ExecContext(ctx,
		`UPDATE watches SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), now, id,
	)
	if err != nil {
		return fmt.Errorf("updating watch status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) UpdateAfterCheck(ctx context.Context, id string, value float64, breaches int, nextCheck time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE watches SET current_value = ?, consecutive_breaches = ?,
		 last_checked_at = ?, next_check_at = ?, updated_at = ? WHERE id = ?`,
		value, breaches, now, nextCheck.Format(time.RFC3339), now, id,
	)
	if err != nil {
		return fmt.Errorf("updating watch after check: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) UpdateBaseline(ctx context.Context, id string, baseline *store.WatchBaseline) error {
	now := time.Now().UTC().Format(time.RFC3339)
	var baselineStr *string
	if baseline != nil {
		b, err := json.Marshal(baseline)
		if err != nil {
			return fmt.Errorf("marshaling baseline: %w", err)
		}
		str := string(b)
		baselineStr = &str
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE watches SET baseline_json = ?, updated_at = ? WHERE id = ?`,
		baselineStr, now, id,
	)
	if err != nil {
		return fmt.Errorf("updating watch baseline: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM watches WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting watch: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) GetDueWatches(ctx context.Context) ([]store.Watch, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+watchColumns+` FROM watches
		 WHERE status = 'active' AND next_check_at IS NOT NULL AND next_check_at <= ?
		 ORDER BY next_check_at ASC`, now,
	)
	if err != nil {
		return nil, fmt.Errorf("querying due watches: %w", err)
	}
	defer rows.Close()

	result := make([]store.Watch, 0)
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning due watch: %w", err)
		}
		result = append(result, *w)
	}
	return result, rows.Err()
}

func (s *watchStore) ExpireWatches(ctx context.Context) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE watches SET status = 'expired', updated_at = ?
		 WHERE expires_at IS NOT NULL AND expires_at <= ? AND status = 'active'`,
		now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("expiring watches: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// --- Run CRUD ---

func (s *watchStore) CreateRun(ctx context.Context, watchID string) (*store.WatchRun, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO watch_runs (id, watch_id, status, started_at) VALUES (?, ?, 'running', ?)`,
		id, watchID, now,
	)
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
	breachedInt := 0
	if breached {
		breachedInt = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE watch_runs SET status = 'completed', metric_value = ?, breached = ?, summary = ?, finished_at = ? WHERE id = ?`,
		value, breachedInt, summary, now, id,
	)
	if err != nil {
		return fmt.Errorf("completing watch run: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) FailRun(ctx context.Context, id string, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE watch_runs SET status = 'failed', error_message = ?, finished_at = ? WHERE id = ?`,
		errMsg, now, id,
	)
	if err != nil {
		return fmt.Errorf("failing watch run: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) ListRuns(ctx context.Context, watchID string, limit int) ([]store.WatchRun, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, watch_id, status, metric_value, breached, summary, error_message, started_at, finished_at
		 FROM watch_runs WHERE watch_id = ? ORDER BY started_at DESC LIMIT ?`,
		watchID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying watch runs: %w", err)
	}
	defer rows.Close()

	result := make([]store.WatchRun, 0)
	for rows.Next() {
		r, err := scanWatchRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning watch run: %w", err)
		}
		result = append(result, *r)
	}
	return result, rows.Err()
}

func scanWatchRun(sc interface{ Scan(...any) error }) (*store.WatchRun, error) {
	r := &store.WatchRun{}
	var metricVal sql.NullFloat64
	var breachedInt int
	var summary, errMsg sql.NullString
	var startedAt string
	var finishedAt sql.NullString

	err := sc.Scan(&r.ID, &r.WatchID, &r.Status, &metricVal, &breachedInt, &summary, &errMsg, &startedAt, &finishedAt)
	if err != nil {
		return nil, err
	}

	if metricVal.Valid {
		r.MetricValue = &metricVal.Float64
	}
	r.Breached = breachedInt != 0
	if summary.Valid {
		r.Summary = summary.String
	}
	if errMsg.Valid {
		r.ErrorMessage = errMsg.String
	}
	r.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
	if finishedAt.Valid {
		t, _ := time.Parse(time.RFC3339, finishedAt.String)
		r.FinishedAt = &t
	}
	return r, nil
}

// --- Alert CRUD ---

func (s *watchStore) CreateAlert(ctx context.Context, params store.CreateWatchAlertParams) (*store.WatchAlert, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	var evidenceStr *string
	if params.Evidence != nil {
		b, err := json.Marshal(params.Evidence)
		if err != nil {
			return nil, fmt.Errorf("marshaling evidence: %w", err)
		}
		str := string(b)
		evidenceStr = &str
	}

	var runID any
	if params.RunID != "" {
		runID = params.RunID
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO watch_alerts (id, watch_id, run_id, urgency, summary, trigger_metric, trigger_value, threshold_value, evidence_json, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`,
		id, params.WatchID, runID, string(params.Urgency), params.Summary,
		params.TriggerMetric, params.TriggerValue, params.ThresholdValue,
		evidenceStr, now,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting watch alert: %w", err)
	}

	return s.GetAlert(ctx, id)
}

func (s *watchStore) GetAlert(ctx context.Context, id string) (*store.WatchAlert, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, watch_id, run_id, urgency, summary, trigger_metric, trigger_value,
		 threshold_value, evidence_json, status, dismiss_reason, created_at
		 FROM watch_alerts WHERE id = ?`, id,
	)
	a, err := scanWatchAlert(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("querying watch alert: %w", err)
	}
	return a, nil
}

func (s *watchStore) ListAlerts(ctx context.Context, watchID string, status string, limit int) ([]store.WatchAlert, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `SELECT id, watch_id, run_id, urgency, summary, trigger_metric, trigger_value,
		 threshold_value, evidence_json, status, dismiss_reason, created_at
		 FROM watch_alerts`
	var conditions []string
	var args []any

	if watchID != "" {
		conditions = append(conditions, `watch_id = ?`)
		args = append(args, watchID)
	}
	if status != "" {
		conditions = append(conditions, `status = ?`)
		args = append(args, status)
	}
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

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

func (s *watchStore) DismissAlert(ctx context.Context, id string, reason string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE watch_alerts SET status = 'dismissed', dismiss_reason = ? WHERE id = ?`,
		reason, id,
	)
	if err != nil {
		return fmt.Errorf("dismissing watch alert: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) AcknowledgeAlert(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE watch_alerts SET status = 'acknowledged' WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("acknowledging watch alert: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *watchStore) CountPendingAlerts(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM watch_alerts WHERE status = 'pending'`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting pending watch alerts: %w", err)
	}
	return count, nil
}
