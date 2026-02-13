package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	watcherType := params.WatcherType
	if watcherType == "" {
		watcherType = WatcherTypeAI
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

	var adaptiveConfigStr *string
	if params.AdaptiveConfig != nil {
		b, err := json.Marshal(params.AdaptiveConfig)
		if err != nil {
			return nil, fmt.Errorf("marshaling adaptive_config: %w", err)
		}
		s := string(b)
		adaptiveConfigStr = &s
	}

	var humanSummaryStr *string
	if params.HumanSummary != nil {
		b, err := json.Marshal(params.HumanSummary)
		if err != nil {
			return nil, fmt.Errorf("marshaling human_summary: %w", err)
		}
		s := string(b)
		humanSummaryStr = &s
	}

	var typeConfigStr *string
	if params.TypeConfig != nil {
		s := string(params.TypeConfig)
		typeConfigStr = &s
	}

	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO watchers (id, title, description, severity, filters, time_range, schedule, model, effort, notify, watcher_type, rule_config, data_source_id, type_config, adaptive_config, human_summary, next_run_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id.String(), params.Title, params.Description, string(severity),
		string(filters), timeRange, params.Schedule, params.Model, string(effort), string(notify),
		string(watcherType), ruleConfigStr, params.DataSourceID, typeConfigStr, adaptiveConfigStr, humanSummaryStr,
		now, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting watcher: %w", err)
	}

	return s.GetByID(ctx, id)
}

// watcherColumns is the SELECT column list for watcher queries.
const watcherColumns = `id, title, description, severity, filters, time_range, schedule, model, effort, status, notify,
	watcher_type, rule_config, data_source_id, type_config,
	last_run_at, next_run_at, last_error, created_at, updated_at,
	adaptive_config, adaptive_state, consecutive_clean_runs, consecutive_errors, escalated_at, base_time_range,
	human_summary`

// scanWatcher scans a watcher row into a Watcher struct.
func scanWatcher(sc interface{ Scan(...any) error }) (*Watcher, error) {
	w := &Watcher{}
	var filtersStr, notifyStr string
	var createdAt, updatedAt string
	var lastRunAt, nextRunAt sql.NullString
	var watcherTypeStr string
	var ruleConfigStr, dataSourceID, typeConfigStr sql.NullString
	var adaptiveConfigStr, escalatedAt, baseTimeRange sql.NullString
	var humanSummaryStr sql.NullString

	err := sc.Scan(
		&w.ID, &w.Title, &w.Description, &w.Severity, &filtersStr,
		&w.TimeRange, &w.Schedule, &w.Model, &w.Effort, &w.Status, &notifyStr,
		&watcherTypeStr, &ruleConfigStr, &dataSourceID, &typeConfigStr,
		&lastRunAt, &nextRunAt, &w.LastError,
		&createdAt, &updatedAt,
		&adaptiveConfigStr, &w.AdaptiveState, &w.ConsecutiveCleanRuns, &w.ConsecutiveErrors,
		&escalatedAt, &baseTimeRange,
		&humanSummaryStr,
	)
	if err != nil {
		return nil, err
	}

	w.Filters = json.RawMessage(filtersStr)
	w.Notify = json.RawMessage(notifyStr)
	w.WatcherType = WatcherType(watcherTypeStr)
	w.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	w.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	if ruleConfigStr.Valid {
		var rc RuleConfig
		if err := json.Unmarshal([]byte(ruleConfigStr.String), &rc); err != nil {
			slog.Warn("invalid rule_config JSON", "watcher_id", w.ID, "error", err)
		} else {
			w.RuleConfig = &rc
		}
	}
	if dataSourceID.Valid {
		w.DataSourceID = &dataSourceID.String
	}
	if typeConfigStr.Valid {
		w.TypeConfig = json.RawMessage(typeConfigStr.String)
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
		if err := json.Unmarshal([]byte(adaptiveConfigStr.String), &ac); err != nil {
			slog.Warn("invalid adaptive_config JSON", "watcher_id", w.ID, "error", err)
		} else {
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
	if humanSummaryStr.Valid {
		var hs WatcherHumanSummary
		if err := json.Unmarshal([]byte(humanSummaryStr.String), &hs); err != nil {
			slog.Warn("invalid human_summary JSON", "watcher_id", w.ID, "error", err)
		} else {
			w.HumanSummary = &hs
		}
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
	query := `SELECT ` + watcherColumns + ` FROM watchers`
	var conditions []string
	var args []any
	if params.WatcherType != "" {
		conditions = append(conditions, `watcher_type = ?`)
		args = append(args, string(params.WatcherType))
	}
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
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

	var titleStr, descStr *string
	var sevStr, effortStr *string
	var filtersStr, timeRangeStr, scheduleStr, modelStr, notifyStr *string
	var watcherTypeStr, ruleConfigStr, dataSourceIDStr *string
	if params.Title != nil {
		titleStr = params.Title
	}
	if params.Description != nil {
		descStr = params.Description
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
	if params.WatcherType != nil {
		s := string(*params.WatcherType)
		watcherTypeStr = &s
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

	var adaptiveConfigStr *string
	if params.AdaptiveConfig != nil {
		b, err := json.Marshal(params.AdaptiveConfig)
		if err != nil {
			return nil, fmt.Errorf("marshaling adaptive_config: %w", err)
		}
		s := string(b)
		adaptiveConfigStr = &s
	}

	var humanSummaryStr *string
	if params.HumanSummary != nil {
		b, err := json.Marshal(params.HumanSummary)
		if err != nil {
			return nil, fmt.Errorf("marshaling human_summary: %w", err)
		}
		s := string(b)
		humanSummaryStr = &s
	}

	var typeConfigUpdateStr *string
	if params.TypeConfig != nil {
		s := string(params.TypeConfig)
		typeConfigUpdateStr = &s
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE watchers
		 SET title            = COALESCE(?, title),
		     description      = COALESCE(?, description),
		     severity         = COALESCE(?, severity),
		     filters          = COALESCE(?, filters),
		     time_range       = COALESCE(?, time_range),
		     schedule         = COALESCE(?, schedule),
		     model            = COALESCE(?, model),
		     effort           = COALESCE(?, effort),
		     notify           = COALESCE(?, notify),
		     watcher_type     = COALESCE(?, watcher_type),
		     rule_config      = COALESCE(?, rule_config),
		     data_source_id   = COALESCE(?, data_source_id),
		     type_config      = COALESCE(?, type_config),
		     adaptive_config  = COALESCE(?, adaptive_config),
		     human_summary    = COALESCE(?, human_summary),
		     updated_at       = ?
		 WHERE id = ?`,
		titleStr, descStr, sevStr, filtersStr, timeRangeStr, scheduleStr, modelStr, effortStr, notifyStr,
		watcherTypeStr, ruleConfigStr, dataSourceIDStr, typeConfigUpdateStr, adaptiveConfigStr, humanSummaryStr,
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

func (s *watcherStore) ResumeWatcher(ctx context.Context, id uuid.UUID) error {
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
		return fmt.Errorf("resuming watcher: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
