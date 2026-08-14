package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type errorGroupStore struct {
	db *bun.DB
}

// NewErrorGroupStore creates a new ErrorGroupStore backed by SQLite.
func NewErrorGroupStore(db *bun.DB) store.ErrorGroupStore {
	return &errorGroupStore{db: db}
}

// Upsert inserts or updates an error group from an ingested log entry.
// If the fingerprint already exists AND is resolved, it reopens the group.
//
// Fast path: a single INSERT ON CONFLICT handles new + existing (non-resolved)
// without a transaction. Only the rare reopen case needs a second statement.
func (s *errorGroupStore) Upsert(ctx context.Context, entry store.LogEntry) error {
	if entry.ErrorFingerprint == "" {
		return nil
	}
	level := strings.ToLower(entry.Level)
	if level != "error" && level != "fatal" {
		return nil
	}

	ts := entry.Timestamp.UTC().Format(time.RFC3339)

	var lastLogID sql.NullInt64
	if entry.ID > 0 {
		lastLogID = sql.NullInt64{Int64: entry.ID, Valid: true}
	}

	// Fast path: single statement per (fingerprint, environment) row.
	// Inserts a new group or increments an existing one scoped to this env.
	_, err := s.db.NewRaw(`
		INSERT INTO error_groups (fingerprint, service, environment, exception_class, message,
			source_file, source_line, first_seen_at, last_seen_at, last_log_id, occurrence_count,
			seen_in_envs)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(fingerprint, environment) DO UPDATE SET
			occurrence_count = occurrence_count + 1,
			last_seen_at = excluded.last_seen_at,
			last_log_id = excluded.last_log_id`,
		entry.ErrorFingerprint, entry.Service, entry.Environment,
		entry.ExceptionClass, truncate(entry.Message, 500),
		entry.SourceFile, entry.SourceLine, ts, ts, lastLogID,
		`["`+entry.Environment+`"]`,
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("upsert error group: %w", err)
	}

	// Maintain seen_in_envs consistency: any row for this fingerprint should
	// list the full set of envs the fingerprint has ever appeared in. Cheap
	// aggregation since rows are keyed by (fingerprint, env) and the count
	// per fingerprint is typically small (one row per env).
	_, err = s.db.NewRaw(`
		UPDATE error_groups SET seen_in_envs = (
			SELECT COALESCE(json_group_array(env), '[]') FROM (
				SELECT DISTINCT environment AS env FROM error_groups WHERE fingerprint = ?
			)
		)
		WHERE fingerprint = ?`,
		entry.ErrorFingerprint, entry.ErrorFingerprint,
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("updating seen_in_envs: %w", err)
	}

	// Slow path: reopen resolved errors scoped to this (fp, env). Only fires
	// when the specific env's row is currently resolved — rare.
	res, err := s.db.NewRaw(`
		UPDATE error_groups SET status = 'unresolved', reopened_count = reopened_count + 1
		WHERE fingerprint = ? AND environment = ? AND status = 'resolved'`,
		entry.ErrorFingerprint, entry.Environment,
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("reopen check: %w", err)
	}
	reopened, _ := res.RowsAffected()
	if reopened > 0 {
		now := rfc3339(time.Now().UTC())
		_, err = s.db.NewRaw(`
			INSERT INTO error_group_events (fingerprint, environment, action, reason, created_at)
			VALUES (?, ?, ?, ?, ?)`,
			entry.ErrorFingerprint, entry.Environment,
			"reopened", "New occurrence after resolution", now,
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("insert reopen event: %w", err)
		}
	}

	return nil
}

// Get returns one row for a fingerprint. Because rows are keyed
// (fingerprint, environment), environment selects which one: pass a concrete
// env for an exact match, or "" to take the most recently seen row across all
// envs. An env-scoped caller must always pass its env — the "" form can return
// a row from an environment the caller may not read.
func (s *errorGroupStore) Get(ctx context.Context, fingerprint, environment string) (*store.ErrorGroup, error) {
	where, args := "WHERE fingerprint = ?", []any{fingerprint}
	if environment != "" {
		where += " AND environment = ?"
		args = append(args, environment)
	}

	var eg errorGroupRow
	err := s.db.NewRaw(`
		SELECT fingerprint, service, environment, exception_class, message,
			source_file, source_line, status, first_seen_at, last_seen_at,
			occurrence_count, last_log_id, reopened_count, resolved_at, ignored_at,
			unique_users, impact_score, COALESCE(common_context, '{}'),
			COALESCE(seen_in_envs, '[]')
		FROM error_groups
		`+where+`
		ORDER BY last_seen_at DESC LIMIT 1`, args...,
	).Scan(ctx,
		&eg.Fingerprint, &eg.Service, &eg.Environment, &eg.ExceptionClass, &eg.Message,
		&eg.SourceFile, &eg.SourceLine, &eg.Status, &eg.FirstSeenAt, &eg.LastSeenAt,
		&eg.OccurrenceCount, &eg.LastLogID, &eg.ReopenedCount, &eg.ResolvedAt, &eg.IgnoredAt,
		&eg.UniqueUsers, &eg.ImpactScore, &eg.CommonContext,
		&eg.SeenInEnvs,
	)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return eg.toStore(), nil
}

func (s *errorGroupStore) List(ctx context.Context, params store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	var qb queryBuilder

	if params.Status != "" {
		qb.where("status = ?", string(params.Status))
	}
	if params.Service != "" {
		qb.where("service = ?", params.Service)
	}
	if params.Environment != "" {
		qb.where("environment = ?", params.Environment)
	}
	if params.Since != nil {
		qb.where("first_seen_at >= ?", params.Since.UTC().Format(time.RFC3339))
	}
	if params.ActiveSince != nil {
		qb.where("last_seen_at >= ?", params.ActiveSince.UTC().Format(time.RFC3339))
	}

	orderBy := "last_seen_at DESC"
	switch params.SortBy {
	case "occurrence_count":
		orderBy = "occurrence_count DESC"
	case "first_seen_at":
		orderBy = "first_seen_at DESC"
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}

	baseQuery := `SELECT fingerprint, service, environment, exception_class, message,
		source_file, source_line, status, first_seen_at, last_seen_at,
		occurrence_count, last_log_id, reopened_count, resolved_at, ignored_at,
		unique_users, impact_score, COALESCE(common_context, '{}'),
		COALESCE(seen_in_envs, '[]')
		FROM error_groups`

	query, args := qb.build(baseQuery)
	query += " ORDER BY " + orderBy
	query += " LIMIT ? OFFSET ?"
	args = append(args, limit, params.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list error groups: %w", err)
	}
	defer rows.Close()

	var groups []store.ErrorGroup
	for rows.Next() {
		var eg errorGroupRow
		if err := rows.Scan(
			&eg.Fingerprint, &eg.Service, &eg.Environment, &eg.ExceptionClass, &eg.Message,
			&eg.SourceFile, &eg.SourceLine, &eg.Status, &eg.FirstSeenAt, &eg.LastSeenAt,
			&eg.OccurrenceCount, &eg.LastLogID, &eg.ReopenedCount, &eg.ResolvedAt, &eg.IgnoredAt,
			&eg.UniqueUsers, &eg.ImpactScore, &eg.CommonContext,
			&eg.SeenInEnvs,
		); err != nil {
			return nil, err
		}
		groups = append(groups, *eg.toStore())
	}
	return groups, rows.Err()
}

// Count returns the number of error groups with the given status. environment
// scopes the count to one env; "" counts across all of them. Passing the
// caller's env matters for more than tidiness: a summary count that includes
// envs the caller cannot list produces a total that disagrees with the rows
// it was shown.
func (s *errorGroupStore) Count(ctx context.Context, status store.ErrorGroupStatus, environment string) (int, error) {
	var qb queryBuilder
	if status != "" {
		qb.where("status = ?", string(status))
	}
	if environment != "" {
		qb.where("environment = ?", environment)
	}
	query, args := qb.build(`SELECT COUNT(*) FROM error_groups`)

	var n int
	err := s.db.NewRaw(query, args...).Scan(ctx, &n)
	return n, err
}

// Resolve / Ignore / Reopen apply the lifecycle transition to one
// (fingerprint, environment) row. environment == "" keeps the old blanket
// semantics — the transition lands on every env-scoped row for the
// fingerprint — which is what unscoped internal callers want and what an
// env-scoped caller must never use.
func (s *errorGroupStore) Resolve(ctx context.Context, fingerprint, environment, reason string) error {
	return s.changeStatus(ctx, fingerprint, environment, "resolved", reason)
}

func (s *errorGroupStore) Ignore(ctx context.Context, fingerprint, environment, reason string) error {
	return s.changeStatus(ctx, fingerprint, environment, "ignored", reason)
}

func (s *errorGroupStore) Reopen(ctx context.Context, fingerprint, environment, reason string) error {
	return s.changeStatus(ctx, fingerprint, environment, "reopened", reason)
}

// changeStatus is the shared implementation. env == "" means "apply to every
// env-scoped row for this fingerprint"; otherwise only the matching row.
//
// The transition is idempotent: rows already in the target state are left
// alone and reported as success, because "already resolved" is not "no such
// group". ErrNotFound is reserved for a fingerprint (or fingerprint+env) that
// genuinely has no row, so a retried resolve does not 404 a group the caller
// can plainly list.
func (s *errorGroupStore) changeStatus(ctx context.Context, fingerprint, environment, action, reason string) error {
	now := rfc3339(time.Now().UTC())

	// statusGuard selects the rows that actually transition; it is applied to
	// both the UPDATE and the event INSERT so no event is written for a row
	// the UPDATE did not touch.
	var setClause, statusGuard string
	switch action {
	case "resolved":
		setClause = "status = 'resolved', resolved_at = ?"
		statusGuard = "status != 'resolved'"
	case "ignored":
		setClause = "status = 'ignored', ignored_at = ?"
		statusGuard = "status != 'ignored'"
	case "reopened":
		setClause = "status = 'unresolved'"
		statusGuard = "status IN ('resolved', 'ignored')"
	default:
		return fmt.Errorf("unknown action %q", action)
	}

	envFilter, envArgs := "", []any{}
	if environment != "" {
		envFilter = " AND environment = ?"
		envArgs = append(envArgs, environment)
	}

	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Existence is checked separately from the transition so "no such
		// group" and "already in that state" stay distinguishable.
		var exists int
		if err := tx.NewRaw(
			`SELECT COUNT(*) FROM error_groups WHERE fingerprint = ?`+envFilter,
			append([]any{fingerprint}, envArgs...)...,
		).Scan(ctx, &exists); err != nil {
			return fmt.Errorf("%s: %w", action, err)
		}
		if exists == 0 {
			return store.ErrNotFound
		}

		// The envs that will actually transition, captured before the UPDATE:
		// the event log must record exactly these and no others.
		var envs []string
		if err := tx.NewRaw(
			`SELECT environment FROM error_groups
			 WHERE fingerprint = ? AND `+statusGuard+envFilter,
			append([]any{fingerprint}, envArgs...)...,
		).Scan(ctx, &envs); err != nil {
			return fmt.Errorf("%s: %w", action, err)
		}
		if len(envs) == 0 {
			// Already in the target state everywhere in scope — nothing to do,
			// and nothing to record. Idempotent success.
			return nil
		}

		args := make([]any, 0, len(envArgs)+2)
		if action != "reopened" {
			args = append(args, now)
		}
		args = append(args, fingerprint)
		args = append(args, envArgs...)

		if _, err := tx.NewRaw(
			`UPDATE error_groups SET `+setClause+
				` WHERE fingerprint = ? AND `+statusGuard+envFilter,
			args...,
		).Exec(ctx); err != nil {
			return fmt.Errorf("%s: %w", action, err)
		}

		for _, env := range envs {
			if _, err := tx.NewRaw(`
				INSERT INTO error_group_events (fingerprint, environment, action, reason, created_at)
				VALUES (?, ?, ?, ?, ?)`,
				fingerprint, env, action, reason, now,
			).Exec(ctx); err != nil {
				return fmt.Errorf("insert %s event: %w", action, err)
			}
		}

		return nil
	})
}

const (
	// defaultEventLimit applies when a caller passes limit <= 0.
	defaultEventLimit = 20
	// maxEventLimit caps ListEvents so a caller cannot pull the whole table.
	maxEventLimit = 500
)

func (s *errorGroupStore) ListEvents(ctx context.Context, fingerprint, environment string, limit int) ([]store.ErrorGroupEvent, error) {
	if limit <= 0 {
		limit = defaultEventLimit
	}
	if limit > maxEventLimit {
		limit = maxEventLimit
	}

	type row struct {
		Fingerprint string `bun:"fingerprint"`
		Environment string `bun:"environment"`
		Action      string `bun:"action"`
		Reason      string `bun:"reason"`
		CreatedAt   string `bun:"created_at"`
	}

	// environment "" means "every env" (unscoped internal callers); a concrete
	// env must not see another environment's lifecycle events.
	query := `
		SELECT fingerprint, environment, action, reason, created_at
		FROM error_group_events
		WHERE fingerprint = ?
		ORDER BY created_at DESC
		LIMIT ?`
	args := []any{fingerprint, limit}
	if environment != "" {
		query = `
		SELECT fingerprint, environment, action, reason, created_at
		FROM error_group_events
		WHERE fingerprint = ? AND environment = ?
		ORDER BY created_at DESC
		LIMIT ?`
		args = []any{fingerprint, environment, limit}
	}

	var rows []row
	err := s.db.NewRaw(query, args...).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	events := make([]store.ErrorGroupEvent, len(rows))
	for i, r := range rows {
		events[i] = store.ErrorGroupEvent{
			Fingerprint: r.Fingerprint,
			Environment: r.Environment,
			Action:      r.Action,
			Reason:      r.Reason,
			CreatedAt:   parseTime(r.CreatedAt),
		}
	}
	return events, nil
}

func (s *errorGroupStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := rfc3339(time.Now().Add(-olderThan))
	res, err := s.db.NewRaw(`DELETE FROM error_groups WHERE last_seen_at < ?`, cutoff).Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()

	// Clean up orphaned error_impacts rows whose (fingerprint, environment)
	// no longer exists in error_groups. FK cascade should handle most of
	// this, but the explicit cleanup guards against rows that predate the
	// cascade or were inserted with FK checks disabled during a migration.
	if n > 0 {
		s.db.NewRaw(`
			DELETE FROM error_impacts
			WHERE (error_fingerprint, environment) NOT IN (
				SELECT fingerprint, environment FROM error_groups
			)`).Exec(ctx)
	}

	return n, nil
}

// errorGroupRow is a scan helper for error group queries.
type errorGroupRow struct {
	Fingerprint     string
	Service         string
	Environment     string
	ExceptionClass  string
	Message         string
	SourceFile      string
	SourceLine      int
	Status          string
	FirstSeenAt     string
	LastSeenAt      string
	OccurrenceCount int
	LastLogID       sql.NullInt64
	ReopenedCount   int
	ResolvedAt      sql.NullString
	IgnoredAt       sql.NullString
	UniqueUsers     int
	ImpactScore     float64
	CommonContext   string
	SeenInEnvs      string
}

func (r *errorGroupRow) toStore() *store.ErrorGroup {
	eg := &store.ErrorGroup{
		Fingerprint:     r.Fingerprint,
		Service:         r.Service,
		Environment:     r.Environment,
		ExceptionClass:  r.ExceptionClass,
		Message:         r.Message,
		SourceFile:      r.SourceFile,
		SourceLine:      r.SourceLine,
		Status:          store.ErrorGroupStatus(r.Status),
		FirstSeenAt:     parseTime(r.FirstSeenAt),
		LastSeenAt:      parseTime(r.LastSeenAt),
		OccurrenceCount: r.OccurrenceCount,
		ReopenedCount:   r.ReopenedCount,
		UniqueUsers:     r.UniqueUsers,
		ImpactScore:     r.ImpactScore,
	}
	if r.LastLogID.Valid {
		eg.LastLogID = &r.LastLogID.Int64
	}
	if r.ResolvedAt.Valid {
		t, _ := time.Parse(time.RFC3339, r.ResolvedAt.String)
		eg.ResolvedAt = &t
	}
	if r.IgnoredAt.Valid {
		t, _ := time.Parse(time.RFC3339, r.IgnoredAt.String)
		eg.IgnoredAt = &t
	}
	if r.CommonContext != "" && r.CommonContext != "{}" {
		json.Unmarshal([]byte(r.CommonContext), &eg.CommonContext)
	}
	if r.SeenInEnvs != "" && r.SeenInEnvs != "[]" {
		var envs []string
		if err := json.Unmarshal([]byte(r.SeenInEnvs), &envs); err == nil {
			eg.SeenInEnvs = envs
		}
	}
	return eg
}

// truncate limits a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
