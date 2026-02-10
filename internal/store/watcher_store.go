package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// watcherStore implements WatcherStore using database/sql (SQLite).
type watcherStore struct {
	db *sql.DB
}

// NewWatcherStore creates a new WatcherStore backed by SQLite.
func NewWatcherStore(db *sql.DB) WatcherStore {
	return &watcherStore{db: db}
}

func (s *watcherStore) Create(ctx context.Context, params CreateWatcherParams) (*Watcher, error) {
	severity := params.Severity
	if severity == "" {
		severity = SeverityWarning
	}
	timeRange := params.TimeRange
	if timeRange == "" {
		timeRange = "15m"
	}
	effort := params.Effort
	if effort == "" {
		effort = EffortMedium
	}
	filters := params.Filters
	if filters == nil {
		filters = json.RawMessage(`{}`)
	}
	notify := params.Notify
	if notify == nil {
		notify = json.RawMessage(`["dashboard"]`)
	}
	monitorType := params.MonitorType
	if monitorType == "" {
		monitorType = MonitorTypeAI
	}

	var ruleConfigStr *string
	if params.RuleConfig != nil {
		b, err := json.Marshal(params.RuleConfig)
		if err != nil {
			return nil, fmt.Errorf("marshaling rule_config: %w", err)
		}
		s := string(b)
		ruleConfigStr = &s
	}

	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO watchers (id, title, description, environment, severity, filters, time_range, schedule, model, effort, notify, monitor_type, rule_config, data_source_id, next_run_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id.String(), params.Title, params.Description, params.Environment, string(severity),
		string(filters), timeRange, params.Schedule, params.Model, string(effort), string(notify),
		string(monitorType), ruleConfigStr, params.DataSourceID,
		now, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting watcher: %w", err)
	}

	return s.GetByID(ctx, id)
}

// watcherColumns is the SELECT column list for watcher queries.
const watcherColumns = `id, title, description, environment, severity, filters, time_range, schedule, model, effort, status, notify,
	monitor_type, rule_config, data_source_id,
	last_run_at, next_run_at, last_error, created_at, updated_at,
	adaptive_config, adaptive_state, consecutive_clean_runs, consecutive_errors, escalated_at, base_time_range`

// scanWatcher scans a watcher row into a Watcher struct.
func scanWatcher(sc interface{ Scan(...any) error }) (*Watcher, error) {
	w := &Watcher{}
	var filtersStr, notifyStr string
	var createdAt, updatedAt string
	var lastRunAt, nextRunAt sql.NullString
	var monitorTypeStr string
	var ruleConfigStr, dataSourceID sql.NullString
	var adaptiveConfigStr, escalatedAt, baseTimeRange sql.NullString

	err := sc.Scan(
		&w.ID, &w.Title, &w.Description, &w.Environment, &w.Severity, &filtersStr,
		&w.TimeRange, &w.Schedule, &w.Model, &w.Effort, &w.Status, &notifyStr,
		&monitorTypeStr, &ruleConfigStr, &dataSourceID,
		&lastRunAt, &nextRunAt, &w.LastError,
		&createdAt, &updatedAt,
		&adaptiveConfigStr, &w.AdaptiveState, &w.ConsecutiveCleanRuns, &w.ConsecutiveErrors,
		&escalatedAt, &baseTimeRange,
	)
	if err != nil {
		return nil, err
	}

	w.Filters = json.RawMessage(filtersStr)
	w.Notify = json.RawMessage(notifyStr)
	w.MonitorType = MonitorType(monitorTypeStr)
	w.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	w.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	if ruleConfigStr.Valid {
		var rc RuleConfig
		if err := json.Unmarshal([]byte(ruleConfigStr.String), &rc); err == nil {
			w.RuleConfig = &rc
		}
	}
	if dataSourceID.Valid {
		w.DataSourceID = &dataSourceID.String
	}
	if lastRunAt.Valid {
		t, _ := time.Parse(time.RFC3339, lastRunAt.String)
		w.LastRunAt = &t
	}
	if nextRunAt.Valid {
		t, _ := time.Parse(time.RFC3339, nextRunAt.String)
		w.NextRunAt = &t
	}
	if adaptiveConfigStr.Valid {
		var ac AdaptiveConfig
		if err := json.Unmarshal([]byte(adaptiveConfigStr.String), &ac); err == nil {
			w.AdaptiveConfig = &ac
		}
	}
	if escalatedAt.Valid {
		t, _ := time.Parse(time.RFC3339, escalatedAt.String)
		w.EscalatedAt = &t
	}
	if baseTimeRange.Valid {
		w.BaseTimeRange = baseTimeRange.String
	}

	return w, nil
}

func (s *watcherStore) GetByID(ctx context.Context, id uuid.UUID) (*Watcher, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+watcherColumns+` FROM watchers WHERE id = ?`, id.String(),
	)
	w, err := scanWatcher(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying watcher: %w", err)
	}
	return w, nil
}

func (s *watcherStore) List(ctx context.Context, params ListWatcherParams) ([]Watcher, error) {
	query := `SELECT ` + watcherColumns + ` FROM watchers WHERE 1=1`
	var args []any
	if params.Environment != "" {
		query += ` AND environment = ?`
		args = append(args, params.Environment)
	}
	if params.MonitorType != "" {
		query += ` AND monitor_type = ?`
		args = append(args, string(params.MonitorType))
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying watchers: %w", err)
	}
	defer rows.Close()

	result := make([]Watcher, 0)
	for rows.Next() {
		w, err := scanWatcher(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning watcher: %w", err)
		}
		result = append(result, *w)
	}

	return result, rows.Err()
}

func (s *watcherStore) Update(ctx context.Context, id uuid.UUID, params UpdateWatcherParams) (*Watcher, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	var titleStr, descStr, envStr *string
	var sevStr, effortStr *string
	var filtersStr, timeRangeStr, scheduleStr, modelStr, notifyStr *string
	var monitorTypeStr, ruleConfigStr, dataSourceIDStr *string
	if params.Title != nil {
		titleStr = params.Title
	}
	if params.Description != nil {
		descStr = params.Description
	}
	if params.Environment != nil {
		envStr = params.Environment
	}
	if params.Severity != nil {
		s := string(*params.Severity)
		sevStr = &s
	}
	if params.Filters != nil {
		s := string(params.Filters)
		filtersStr = &s
	}
	if params.TimeRange != nil {
		timeRangeStr = params.TimeRange
	}
	if params.Schedule != nil {
		scheduleStr = params.Schedule
	}
	if params.Model != nil {
		modelStr = params.Model
	}
	if params.Effort != nil {
		s := string(*params.Effort)
		effortStr = &s
	}
	if params.Notify != nil {
		s := string(params.Notify)
		notifyStr = &s
	}
	if params.MonitorType != nil {
		s := string(*params.MonitorType)
		monitorTypeStr = &s
	}
	if params.RuleConfig != nil {
		b, err := json.Marshal(params.RuleConfig)
		if err != nil {
			return nil, fmt.Errorf("marshaling rule_config: %w", err)
		}
		s := string(b)
		ruleConfigStr = &s
	}
	if params.DataSourceID != nil {
		dataSourceIDStr = params.DataSourceID
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE watchers
		 SET title          = COALESCE(?, title),
		     description    = COALESCE(?, description),
		     environment    = COALESCE(?, environment),
		     severity       = COALESCE(?, severity),
		     filters        = COALESCE(?, filters),
		     time_range     = COALESCE(?, time_range),
		     schedule       = COALESCE(?, schedule),
		     model          = COALESCE(?, model),
		     effort         = COALESCE(?, effort),
		     notify         = COALESCE(?, notify),
		     monitor_type   = COALESCE(?, monitor_type),
		     rule_config    = COALESCE(?, rule_config),
		     data_source_id = COALESCE(?, data_source_id),
		     updated_at     = ?
		 WHERE id = ?`,
		titleStr, descStr, envStr, sevStr, filtersStr, timeRangeStr, scheduleStr, modelStr, effortStr, notifyStr,
		monitorTypeStr, ruleConfigStr, dataSourceIDStr,
		now, id.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("updating watcher: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}

	return s.GetByID(ctx, id)
}

func (s *watcherStore) UpdateStatus(ctx context.Context, id uuid.UUID, status WatcherStatus) (*Watcher, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE watchers SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), now, id.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("updating watcher status: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.GetByID(ctx, id)
}

func (s *watcherStore) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM watchers WHERE id = ?`, id.String())
	if err != nil {
		return fmt.Errorf("deleting watcher: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *watcherStore) GetDueWatchers(ctx context.Context) ([]Watcher, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+watcherColumns+`
		 FROM watchers
		 WHERE status = 'active' AND next_run_at <= ?
		 ORDER BY next_run_at ASC`, now,
	)
	if err != nil {
		return nil, fmt.Errorf("querying due watchers: %w", err)
	}
	defer rows.Close()

	result := make([]Watcher, 0)
	for rows.Next() {
		w, err := scanWatcher(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning due watcher: %w", err)
		}
		result = append(result, *w)
	}

	return result, rows.Err()
}

func (s *watcherStore) UpdateRunTime(ctx context.Context, id uuid.UUID, lastRun, nextRun time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE watchers SET last_run_at = ?, next_run_at = ?, updated_at = ? WHERE id = ?`,
		lastRun.UTC().Format(time.RFC3339), nextRun.UTC().Format(time.RFC3339), now, id.String(),
	)
	if err != nil {
		return fmt.Errorf("updating watcher run time: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *watcherStore) UpdateAdaptiveState(ctx context.Context, id uuid.UUID, params UpdateAdaptiveParams) error {
	now := time.Now().UTC().Format(time.RFC3339)

	var escalatedAtStr *string
	if params.EscalatedAt != nil {
		s := params.EscalatedAt.UTC().Format(time.RFC3339)
		escalatedAtStr = &s
	}

	var baseTimeRangeStr *string
	if params.BaseTimeRange != "" {
		baseTimeRangeStr = &params.BaseTimeRange
	}

	var timeRangeStr *string
	if params.TimeRange != "" {
		timeRangeStr = &params.TimeRange
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE watchers
		 SET adaptive_state = ?,
		     consecutive_clean_runs = ?,
		     consecutive_errors = ?,
		     escalated_at = COALESCE(?, escalated_at),
		     base_time_range = COALESCE(?, base_time_range),
		     time_range = COALESCE(?, time_range),
		     updated_at = ?
		 WHERE id = ?`,
		string(params.AdaptiveState),
		params.ConsecutiveCleanRuns,
		params.ConsecutiveErrors,
		escalatedAtStr,
		baseTimeRangeStr,
		timeRangeStr,
		now, id.String(),
	)
	if err != nil {
		return fmt.Errorf("updating adaptive state: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *watcherStore) ResumeMonitor(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE watchers
		 SET adaptive_state = 'normal',
		     consecutive_errors = 0,
		     consecutive_clean_runs = 0,
		     escalated_at = NULL,
		     next_run_at = ?,
		     status = 'active',
		     updated_at = ?
		 WHERE id = ? AND adaptive_state = 'error'`,
		now, now, id.String(),
	)
	if err != nil {
		return fmt.Errorf("resuming monitor: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
