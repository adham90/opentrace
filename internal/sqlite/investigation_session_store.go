package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/pkg/store"
)

// investigationSessionColumns is the canonical column list for SELECT queries.
const investigationSessionColumns = `id, user_id, user_email, user_role,
		client_name, client_version, workspace, transport, connection_id,
		intent, intent_detail, primary_service, primary_datasource_id,
		status, summary, root_cause, fix_description,
		created_watcher_ids, triggered_by_alert_id, triggered_by_watcher_id,
		resolved_error_group_ids, investigated_error_fingerprints,
		created_healthcheck_ids, triggered_by_healthcheck_id,
		created_note_ids, auto_note_ids,
		runbooks_executed, explained_queries, killed_queries, trace_ids,
		correlated_deploy, pre_investigation_snapshot, post_investigation_snapshot,
		total_steps, total_errors, tool_sequence, tool_fingerprint, arg_signature,
		started_at, last_activity_at, ended_at, duration_seconds,
		recurrence_group, recurrence_count, previous_session_id, fix_durability_seconds,
		files_modified, files_read, linked_deploy_id`

type investigationSessionStore struct {
	db *sql.DB
}

// NewInvestigationSessionStore creates a new InvestigationSessionStore backed by SQLite.
func NewInvestigationSessionStore(db *sql.DB) store.InvestigationSessionStore {
	return &investigationSessionStore{db: db}
}

func (s *investigationSessionStore) Create(ctx context.Context, params store.CreateInvestigationSessionParams) (*store.InvestigationSession, error) {
	id := uuid.New().String()
	connID := params.ConnectionID
	if connID == "" {
		connID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO investigation_sessions
		 (id, user_id, user_email, user_role, client_name, client_version,
		  workspace, transport, connection_id, status, started_at, last_activity_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?, ?)`,
		id, params.UserID, params.UserEmail, params.UserRole,
		params.ClientName, params.ClientVersion,
		params.Workspace, params.Transport, connID,
		now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("creating investigation session: %w", err)
	}

	return s.GetByID(ctx, id)
}

func (s *investigationSessionStore) GetByID(ctx context.Context, id string) (*store.InvestigationSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+investigationSessionColumns+`
		 FROM investigation_sessions
		 WHERE id = ?`, id)

	sess, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting investigation session: %w", err)
	}
	return sess, nil
}

func (s *investigationSessionStore) Close(ctx context.Context, id string) error {
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	// Calculate duration from started_at.
	var startedAtStr string
	err := s.db.QueryRowContext(ctx,
		`SELECT started_at FROM investigation_sessions WHERE id = ?`, id,
	).Scan(&startedAtStr)
	if err == sql.ErrNoRows {
		return store.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("closing investigation session: %w", err)
	}

	startedAt, _ := time.Parse(time.RFC3339, startedAtStr)
	duration := int(now.Sub(startedAt).Seconds())

	_, err = s.db.ExecContext(ctx,
		`UPDATE investigation_sessions
		 SET ended_at = ?, duration_seconds = ?, last_activity_at = ?,
		     status = CASE WHEN status = 'open' THEN 'unresolved' ELSE status END
		 WHERE id = ?`,
		nowStr, duration, nowStr, id,
	)
	if err != nil {
		return fmt.Errorf("closing investigation session: %w", err)
	}
	return nil
}

func (s *investigationSessionStore) Update(ctx context.Context, id string, params store.UpdateInvestigationSessionParams) error {
	var sets []string
	var args []any

	if params.Intent != nil {
		sets = append(sets, "intent = ?")
		args = append(args, *params.Intent)
	}
	if params.IntentDetail != nil {
		sets = append(sets, "intent_detail = ?")
		args = append(args, *params.IntentDetail)
	}
	if params.PrimaryService != nil {
		sets = append(sets, "primary_service = ?")
		args = append(args, *params.PrimaryService)
	}
	if params.PrimaryDatasourceID != nil {
		sets = append(sets, "primary_datasource_id = ?")
		args = append(args, *params.PrimaryDatasourceID)
	}
	if params.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, string(*params.Status))
	}
	if params.Summary != nil {
		sets = append(sets, "summary = ?")
		args = append(args, *params.Summary)
	}
	if params.RootCause != nil {
		sets = append(sets, "root_cause = ?")
		args = append(args, *params.RootCause)
	}
	if params.FixDescription != nil {
		sets = append(sets, "fix_description = ?")
		args = append(args, *params.FixDescription)
	}
	if params.TotalSteps != nil {
		sets = append(sets, "total_steps = ?")
		args = append(args, *params.TotalSteps)
	}
	if params.TotalErrors != nil {
		sets = append(sets, "total_errors = ?")
		args = append(args, *params.TotalErrors)
	}
	if params.ToolSequence != nil {
		seqJSON, _ := json.Marshal(params.ToolSequence)
		sets = append(sets, "tool_sequence = ?")
		args = append(args, string(seqJSON))
	}
	if params.ToolFingerprint != nil {
		sets = append(sets, "tool_fingerprint = ?")
		args = append(args, *params.ToolFingerprint)
	}
	if params.ArgSignature != nil {
		sets = append(sets, "arg_signature = ?")
		args = append(args, *params.ArgSignature)
	}

	// Subsystem links
	if params.CreatedWatcherIDs != nil {
		j, _ := json.Marshal(params.CreatedWatcherIDs)
		sets = append(sets, "created_watcher_ids = ?")
		args = append(args, string(j))
	}
	if params.TriggeredByWatcherID != nil {
		sets = append(sets, "triggered_by_watcher_id = ?")
		args = append(args, *params.TriggeredByWatcherID)
	}
	if params.ResolvedErrorGroupIDs != nil {
		j, _ := json.Marshal(params.ResolvedErrorGroupIDs)
		sets = append(sets, "resolved_error_group_ids = ?")
		args = append(args, string(j))
	}
	if params.InvestigatedErrorFingerprints != nil {
		j, _ := json.Marshal(params.InvestigatedErrorFingerprints)
		sets = append(sets, "investigated_error_fingerprints = ?")
		args = append(args, string(j))
	}
	if params.CreatedHealthcheckIDs != nil {
		j, _ := json.Marshal(params.CreatedHealthcheckIDs)
		sets = append(sets, "created_healthcheck_ids = ?")
		args = append(args, string(j))
	}
	if params.TriggeredByHealthcheckID != nil {
		sets = append(sets, "triggered_by_healthcheck_id = ?")
		args = append(args, *params.TriggeredByHealthcheckID)
	}

	// Stage 4: Additional subsystem links
	if params.RunbooksExecuted != nil {
		j, _ := json.Marshal(params.RunbooksExecuted)
		sets = append(sets, "runbooks_executed = ?")
		args = append(args, string(j))
	}
	if params.ExplainedQueries != nil {
		j, _ := json.Marshal(params.ExplainedQueries)
		sets = append(sets, "explained_queries = ?")
		args = append(args, string(j))
	}
	if params.KilledQueries != nil {
		j, _ := json.Marshal(params.KilledQueries)
		sets = append(sets, "killed_queries = ?")
		args = append(args, string(j))
	}
	if params.TraceIDs != nil {
		j, _ := json.Marshal(params.TraceIDs)
		sets = append(sets, "trace_ids = ?")
		args = append(args, string(j))
	}
	if params.CorrelatedDeploy != nil {
		sets = append(sets, "correlated_deploy = ?")
		args = append(args, *params.CorrelatedDeploy)
	}
	if params.PreInvestigationSnapshot != nil {
		sets = append(sets, "pre_investigation_snapshot = ?")
		args = append(args, *params.PreInvestigationSnapshot)
	}
	if params.PostInvestigationSnapshot != nil {
		sets = append(sets, "post_investigation_snapshot = ?")
		args = append(args, *params.PostInvestigationSnapshot)
	}
	if params.AutoNoteIDs != nil {
		j, _ := json.Marshal(params.AutoNoteIDs)
		sets = append(sets, "auto_note_ids = ?")
		args = append(args, string(j))
	}
	if params.CreatedNoteIDs != nil {
		j, _ := json.Marshal(params.CreatedNoteIDs)
		sets = append(sets, "created_note_ids = ?")
		args = append(args, string(j))
	}

	// Recurrence
	if params.RecurrenceGroup != nil {
		sets = append(sets, "recurrence_group = ?")
		args = append(args, *params.RecurrenceGroup)
	}
	if params.RecurrenceCount != nil {
		sets = append(sets, "recurrence_count = ?")
		args = append(args, *params.RecurrenceCount)
	}
	if params.PreviousSessionID != nil {
		sets = append(sets, "previous_session_id = ?")
		args = append(args, *params.PreviousSessionID)
	}
	if params.FixDurabilitySeconds != nil {
		sets = append(sets, "fix_durability_seconds = ?")
		args = append(args, *params.FixDurabilitySeconds)
	}

	// Stage 6: Development session tracking
	if params.FilesModified != nil {
		j, _ := json.Marshal(params.FilesModified)
		sets = append(sets, "files_modified = ?")
		args = append(args, string(j))
	}
	if params.FilesRead != nil {
		j, _ := json.Marshal(params.FilesRead)
		sets = append(sets, "files_read = ?")
		args = append(args, string(j))
	}
	if params.LinkedDeployID != nil {
		sets = append(sets, "linked_deploy_id = ?")
		args = append(args, *params.LinkedDeployID)
	}

	if len(sets) == 0 {
		return nil
	}

	// Always update last_activity_at.
	sets = append(sets, "last_activity_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))

	args = append(args, id)
	query := fmt.Sprintf("UPDATE investigation_sessions SET %s WHERE id = ?", strings.Join(sets, ", "))

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating investigation session: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *investigationSessionStore) FindRecent(ctx context.Context, params store.FindRecentSessionParams) (*store.InvestigationSession, error) {
	cutoff := time.Now().UTC().Add(-params.MaxAge).Format(time.RFC3339)
	status := string(params.Status)
	if status == "" {
		status = "open"
	}

	row := s.db.QueryRowContext(ctx,
		`SELECT `+investigationSessionColumns+`
		 FROM investigation_sessions
		 WHERE user_id = ? AND workspace = ? AND status = ? AND last_activity_at > ?
		 ORDER BY last_activity_at DESC
		 LIMIT 1`,
		params.UserID, params.Workspace, status, cutoff,
	)

	sess, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, nil // no recent session found, not an error
	}
	if err != nil {
		return nil, fmt.Errorf("finding recent investigation session: %w", err)
	}
	return sess, nil
}

func (s *investigationSessionStore) List(ctx context.Context, params store.ListInvestigationSessionParams) ([]store.InvestigationSession, error) {
	var where []string
	var args []any

	if params.UserID != "" {
		where = append(where, "user_id = ?")
		args = append(args, params.UserID)
	}
	if params.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(params.Status))
	}
	if params.Intent != "" {
		where = append(where, "intent = ?")
		args = append(args, params.Intent)
	}
	if params.Service != "" {
		where = append(where, "primary_service = ?")
		args = append(args, params.Service)
	}
	if !params.Since.IsZero() {
		where = append(where, "started_at >= ?")
		args = append(args, params.Since.Format(time.RFC3339))
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}

	query := fmt.Sprintf(
		`SELECT `+investigationSessionColumns+`
		 FROM investigation_sessions
		 %s
		 ORDER BY started_at DESC
		 LIMIT ? OFFSET ?`,
		whereClause,
	)
	args = append(args, limit, params.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing investigation sessions: %w", err)
	}
	defer rows.Close()

	var sessions []store.InvestigationSession
	for rows.Next() {
		sess, err := scanSessionRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning investigation session: %w", err)
		}
		sessions = append(sessions, *sess)
	}
	return sessions, rows.Err()
}

func (s *investigationSessionStore) Stats(ctx context.Context) (*store.InvestigationSessionStats, error) {
	stats := &store.InvestigationSessionStats{}

	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM investigation_sessions`,
	).Scan(&stats.TotalSessions)
	if err != nil {
		return nil, fmt.Errorf("counting sessions: %w", err)
	}

	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM investigation_sessions WHERE status = 'open'`,
	).Scan(&stats.OpenSessions)
	if err != nil {
		return nil, fmt.Errorf("counting open sessions: %w", err)
	}

	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM investigation_sessions WHERE status = 'resolved'`,
	).Scan(&stats.ResolvedSessions)
	if err != nil {
		return nil, fmt.Errorf("counting resolved sessions: %w", err)
	}

	var avgSteps, avgDuration sql.NullFloat64
	err = s.db.QueryRowContext(ctx,
		`SELECT AVG(total_steps), AVG(duration_seconds) FROM investigation_sessions WHERE status != 'open'`,
	).Scan(&avgSteps, &avgDuration)
	if err != nil {
		return nil, fmt.Errorf("computing session averages: %w", err)
	}
	if avgSteps.Valid {
		stats.AvgSteps = avgSteps.Float64
	}
	if avgDuration.Valid {
		stats.AvgDurationSecs = avgDuration.Float64
	}

	return stats, nil
}

func (s *investigationSessionStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		result, err := s.db.ExecContext(ctx,
			`DELETE FROM investigation_sessions
			 WHERE rowid IN (SELECT rowid FROM investigation_sessions WHERE ended_at IS NOT NULL AND ended_at < ? LIMIT 1000)`,
			cutoff,
		)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning investigation sessions: %w", err)
		}
		n, _ := result.RowsAffected()
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}

func (s *investigationSessionStore) RecordStep(ctx context.Context, sessionID string, toolName string, isError bool) error {
	now := time.Now().UTC().Format(time.RFC3339)

	errIncr := 0
	if isError {
		errIncr = 1
	}

	// Atomically increment step count and append to tool_sequence.
	// Read current tool_sequence, append, write back.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("recording step: %w", err)
	}
	defer tx.Rollback()

	var seqJSON string
	err = tx.QueryRowContext(ctx,
		`SELECT tool_sequence FROM investigation_sessions WHERE id = ?`, sessionID,
	).Scan(&seqJSON)
	if err == sql.ErrNoRows {
		return store.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("recording step: %w", err)
	}

	var seq []string
	if seqJSON != "" && seqJSON != "[]" {
		json.Unmarshal([]byte(seqJSON), &seq)
	}
	seq = append(seq, toolName)
	newSeqJSON, _ := json.Marshal(seq)
	fingerprint := strings.Join(seq, "|")

	_, err = tx.ExecContext(ctx,
		`UPDATE investigation_sessions
		 SET total_steps = total_steps + 1,
		     total_errors = total_errors + ?,
		     tool_sequence = ?,
		     tool_fingerprint = ?,
		     last_activity_at = ?
		 WHERE id = ?`,
		errIncr, string(newSeqJSON), fingerprint, now, sessionID,
	)
	if err != nil {
		return fmt.Errorf("recording step: %w", err)
	}

	return tx.Commit()
}

// FindByCreatedWatcher finds the most recent session that created the given watcher.
// Returns nil, nil if no matching session is found.
func (s *investigationSessionStore) FindByCreatedWatcher(ctx context.Context, watcherID string) (*store.InvestigationSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+investigationSessionColumns+`
		 FROM investigation_sessions
		 WHERE created_watcher_ids LIKE '%"' || ? || '"%'
		 ORDER BY ended_at DESC
		 LIMIT 1`, watcherID)

	sess, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding session by created watcher: %w", err)
	}
	return sess, nil
}

// FindByResolvedError finds the most recent session that resolved the given error fingerprint.
// Returns nil, nil if no matching session is found.
func (s *investigationSessionStore) FindByResolvedError(ctx context.Context, fingerprint string) (*store.InvestigationSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+investigationSessionColumns+`
		 FROM investigation_sessions
		 WHERE resolved_error_group_ids LIKE '%"' || ? || '"%'
		 ORDER BY ended_at DESC
		 LIMIT 1`, fingerprint)

	sess, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding session by resolved error: %w", err)
	}
	return sess, nil
}

// FindByCreatedHealthcheck finds the most recent session that created the given health check.
// Returns nil, nil if no matching session is found.
func (s *investigationSessionStore) FindByCreatedHealthcheck(ctx context.Context, healthcheckID string) (*store.InvestigationSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+investigationSessionColumns+`
		 FROM investigation_sessions
		 WHERE created_healthcheck_ids LIKE '%"' || ? || '"%'
		 ORDER BY ended_at DESC
		 LIMIT 1`, healthcheckID)

	sess, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding session by created healthcheck: %w", err)
	}
	return sess, nil
}

// scanSession scans a single row into an InvestigationSession.
func scanSession(row *sql.Row) (*store.InvestigationSession, error) {
	var sess store.InvestigationSession
	var dsID sql.NullInt64
	var seqJSON, startedAt, lastActivityAt string
	var endedAt sql.NullString
	var createdWatcherJSON, resolvedErrorJSON, investigatedErrorJSON, createdHCJSON string
	var triggeredByAlert, triggeredByWatcher, triggeredByHC sql.NullString
	var createdNoteJSON, autoNoteJSON string
	var runbooksJSON, explainedJSON, killedJSON, traceIDsJSON string
	var preSnapshotJSON, postSnapshotJSON string
	var recurrenceGroup, previousSessionID sql.NullString
	var fixDurability sql.NullInt64
	var filesModifiedJSON, filesReadJSON string
	var linkedDeployID sql.NullInt64

	err := row.Scan(
		&sess.ID, &sess.UserID, &sess.UserEmail, &sess.UserRole,
		&sess.ClientName, &sess.ClientVersion, &sess.Workspace, &sess.Transport, &sess.ConnectionID,
		&sess.Intent, &sess.IntentDetail, &sess.PrimaryService, &dsID,
		&sess.Status, &sess.Summary, &sess.RootCause, &sess.FixDescription,
		&createdWatcherJSON, &triggeredByAlert, &triggeredByWatcher,
		&resolvedErrorJSON, &investigatedErrorJSON,
		&createdHCJSON, &triggeredByHC,
		&createdNoteJSON, &autoNoteJSON,
		&runbooksJSON, &explainedJSON, &killedJSON, &traceIDsJSON,
		&sess.CorrelatedDeploy, &preSnapshotJSON, &postSnapshotJSON,
		&sess.TotalSteps, &sess.TotalErrors, &seqJSON, &sess.ToolFingerprint, &sess.ArgSignature,
		&startedAt, &lastActivityAt, &endedAt, &sess.DurationSeconds,
		&recurrenceGroup, &sess.RecurrenceCount, &previousSessionID, &fixDurability,
		&filesModifiedJSON, &filesReadJSON, &linkedDeployID,
	)
	if err != nil {
		return nil, err
	}

	if dsID.Valid {
		v := int(dsID.Int64)
		sess.PrimaryDatasourceID = &v
	}

	// JSON array columns
	unmarshalStringSlice := func(raw string) []string {
		var s []string
		if raw != "" && raw != "[]" {
			if err := json.Unmarshal([]byte(raw), &s); err != nil {
				slog.Warn("investigation_session: malformed JSON array", "error", err)
			}
		}
		if s == nil {
			return []string{}
		}
		return s
	}

	sess.ToolSequence = unmarshalStringSlice(seqJSON)
	sess.CreatedWatcherIDs = unmarshalStringSlice(createdWatcherJSON)
	sess.ResolvedErrorGroupIDs = unmarshalStringSlice(resolvedErrorJSON)
	sess.InvestigatedErrorFingerprints = unmarshalStringSlice(investigatedErrorJSON)
	sess.CreatedHealthcheckIDs = unmarshalStringSlice(createdHCJSON)
	sess.CreatedNoteIDs = unmarshalStringSlice(createdNoteJSON)
	sess.AutoNoteIDs = unmarshalStringSlice(autoNoteJSON)
	sess.RunbooksExecuted = unmarshalStringSlice(runbooksJSON)
	sess.ExplainedQueries = unmarshalStringSlice(explainedJSON)
	sess.KilledQueries = unmarshalStringSlice(killedJSON)
	sess.TraceIDs = unmarshalStringSlice(traceIDsJSON)
	sess.FilesModified = unmarshalStringSlice(filesModifiedJSON)
	sess.FilesRead = unmarshalStringSlice(filesReadJSON)

	// JSON map columns
	json.Unmarshal([]byte(preSnapshotJSON), &sess.PreInvestigationSnapshot)
	json.Unmarshal([]byte(postSnapshotJSON), &sess.PostInvestigationSnapshot)

	// Nullable TEXT columns
	if triggeredByAlert.Valid {
		sess.TriggeredByAlertID = &triggeredByAlert.String
	}
	if triggeredByWatcher.Valid {
		sess.TriggeredByWatcherID = &triggeredByWatcher.String
	}
	if triggeredByHC.Valid {
		sess.TriggeredByHealthcheckID = &triggeredByHC.String
	}
	if linkedDeployID.Valid {
		sess.LinkedDeployID = &linkedDeployID.Int64
	}

	sess.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
	sess.LastActivityAt, _ = time.Parse(time.RFC3339, lastActivityAt)
	if endedAt.Valid {
		t, _ := time.Parse(time.RFC3339, endedAt.String)
		if !t.IsZero() {
			sess.EndedAt = &t
		}
	}
	if recurrenceGroup.Valid {
		sess.RecurrenceGroup = &recurrenceGroup.String
	}
	if previousSessionID.Valid {
		sess.PreviousSessionID = &previousSessionID.String
	}
	if fixDurability.Valid {
		v := int(fixDurability.Int64)
		sess.FixDurabilitySeconds = &v
	}

	return &sess, nil
}

// nthPipe returns the index of the nth '|' in s, or -1 if not found.
func nthPipe(s string, n int) int {
	count := 0
	for i, c := range s {
		if c == '|' {
			count++
			if count == n {
				return i
			}
		}
	}
	return -1
}

func (s *investigationSessionStore) FindSimilar(ctx context.Context, params store.FindSimilarParams) ([]store.InvestigationSession, error) {
	maxResults := params.MaxResults
	if maxResults <= 0 {
		maxResults = 3
	}
	minSteps := params.MinSteps
	if minSteps <= 0 {
		minSteps = 1
	}

	// Build dynamic WHERE clause
	where := "WHERE id != ?"
	args := []any{params.ExcludeSessionID}

	if params.Service != "" {
		where += " AND primary_service = ?"
		args = append(args, params.Service)
	}
	if params.Intent != "" {
		where += " AND intent = ?"
		args = append(args, params.Intent)
	}
	if params.OnlyResolved {
		where += " AND status = 'resolved'"
	}
	where += " AND total_steps >= ?"
	args = append(args, minSteps)

	// If a tool fingerprint is provided, prefer sessions with a common prefix
	if params.ToolFingerprint != "" {
		prefix := params.ToolFingerprint
		if idx := nthPipe(prefix, 3); idx > 0 {
			prefix = prefix[:idx]
		}
		where += " AND tool_fingerprint LIKE ?"
		args = append(args, prefix+"%")
	}

	args = append(args, maxResults)

	query := fmt.Sprintf(
		`SELECT %s FROM investigation_sessions %s ORDER BY started_at DESC LIMIT ?`,
		investigationSessionColumns, where,
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("finding similar sessions: %w", err)
	}
	defer rows.Close()

	var result []store.InvestigationSession
	for rows.Next() {
		sess, err := scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *sess)
	}
	return result, rows.Err()
}

// scanSessionRow scans a *sql.Rows row (used in List).
func scanSessionRow(rows *sql.Rows) (*store.InvestigationSession, error) {
	var sess store.InvestigationSession
	var dsID sql.NullInt64
	var seqJSON, startedAt, lastActivityAt string
	var endedAt sql.NullString
	var createdWatcherJSON, resolvedErrorJSON, investigatedErrorJSON, createdHCJSON string
	var triggeredByAlert, triggeredByWatcher, triggeredByHC sql.NullString
	var createdNoteJSON, autoNoteJSON string
	var runbooksJSON, explainedJSON, killedJSON, traceIDsJSON string
	var preSnapshotJSON, postSnapshotJSON string
	var recurrenceGroup, previousSessionID sql.NullString
	var fixDurability sql.NullInt64
	var filesModifiedJSON, filesReadJSON string
	var linkedDeployID sql.NullInt64

	err := rows.Scan(
		&sess.ID, &sess.UserID, &sess.UserEmail, &sess.UserRole,
		&sess.ClientName, &sess.ClientVersion, &sess.Workspace, &sess.Transport, &sess.ConnectionID,
		&sess.Intent, &sess.IntentDetail, &sess.PrimaryService, &dsID,
		&sess.Status, &sess.Summary, &sess.RootCause, &sess.FixDescription,
		&createdWatcherJSON, &triggeredByAlert, &triggeredByWatcher,
		&resolvedErrorJSON, &investigatedErrorJSON,
		&createdHCJSON, &triggeredByHC,
		&createdNoteJSON, &autoNoteJSON,
		&runbooksJSON, &explainedJSON, &killedJSON, &traceIDsJSON,
		&sess.CorrelatedDeploy, &preSnapshotJSON, &postSnapshotJSON,
		&sess.TotalSteps, &sess.TotalErrors, &seqJSON, &sess.ToolFingerprint, &sess.ArgSignature,
		&startedAt, &lastActivityAt, &endedAt, &sess.DurationSeconds,
		&recurrenceGroup, &sess.RecurrenceCount, &previousSessionID, &fixDurability,
		&filesModifiedJSON, &filesReadJSON, &linkedDeployID,
	)
	if err != nil {
		return nil, err
	}

	if dsID.Valid {
		v := int(dsID.Int64)
		sess.PrimaryDatasourceID = &v
	}

	// JSON array columns
	unmarshalStringSlice := func(raw string) []string {
		var s []string
		if raw != "" && raw != "[]" {
			if err := json.Unmarshal([]byte(raw), &s); err != nil {
				slog.Warn("investigation_session: malformed JSON array", "error", err)
			}
		}
		if s == nil {
			return []string{}
		}
		return s
	}

	sess.ToolSequence = unmarshalStringSlice(seqJSON)
	sess.CreatedWatcherIDs = unmarshalStringSlice(createdWatcherJSON)
	sess.ResolvedErrorGroupIDs = unmarshalStringSlice(resolvedErrorJSON)
	sess.InvestigatedErrorFingerprints = unmarshalStringSlice(investigatedErrorJSON)
	sess.CreatedHealthcheckIDs = unmarshalStringSlice(createdHCJSON)
	sess.CreatedNoteIDs = unmarshalStringSlice(createdNoteJSON)
	sess.AutoNoteIDs = unmarshalStringSlice(autoNoteJSON)
	sess.RunbooksExecuted = unmarshalStringSlice(runbooksJSON)
	sess.ExplainedQueries = unmarshalStringSlice(explainedJSON)
	sess.KilledQueries = unmarshalStringSlice(killedJSON)
	sess.TraceIDs = unmarshalStringSlice(traceIDsJSON)
	sess.FilesModified = unmarshalStringSlice(filesModifiedJSON)
	sess.FilesRead = unmarshalStringSlice(filesReadJSON)

	// JSON map columns
	json.Unmarshal([]byte(preSnapshotJSON), &sess.PreInvestigationSnapshot)
	json.Unmarshal([]byte(postSnapshotJSON), &sess.PostInvestigationSnapshot)

	// Nullable TEXT columns
	if triggeredByAlert.Valid {
		sess.TriggeredByAlertID = &triggeredByAlert.String
	}
	if triggeredByWatcher.Valid {
		sess.TriggeredByWatcherID = &triggeredByWatcher.String
	}
	if triggeredByHC.Valid {
		sess.TriggeredByHealthcheckID = &triggeredByHC.String
	}
	if linkedDeployID.Valid {
		sess.LinkedDeployID = &linkedDeployID.Int64
	}

	sess.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
	sess.LastActivityAt, _ = time.Parse(time.RFC3339, lastActivityAt)
	if endedAt.Valid {
		t, _ := time.Parse(time.RFC3339, endedAt.String)
		if !t.IsZero() {
			sess.EndedAt = &t
		}
	}
	if recurrenceGroup.Valid {
		sess.RecurrenceGroup = &recurrenceGroup.String
	}
	if previousSessionID.Valid {
		sess.PreviousSessionID = &previousSessionID.String
	}
	if fixDurability.Valid {
		v := int(fixDurability.Int64)
		sess.FixDurabilitySeconds = &v
	}

	return &sess, nil
}
