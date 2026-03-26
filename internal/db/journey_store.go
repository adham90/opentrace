package db

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

type journeyStore struct {
	db *bun.DB
}

// NewJourneyStore creates a JourneyStore backed by SQLite.
func NewJourneyStore(db *bun.DB) store.JourneyStore {
	return &journeyStore{db: db}
}

// BuildSessions scans logs with session_id and builds/updates user_sessions.
func (s *journeyStore) BuildSessions(ctx context.Context, since time.Time) error {
	sinceStr := since.Format(time.RFC3339)

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			agg.session_id,
			agg.user_id,
			agg.service,
			agg.env,
			agg.started_at,
			agg.ended_at,
			agg.request_count,
			agg.error_count,
			agg.total_duration_ms,
			COALESCE((
				SELECT COALESCE(rs2.controller || '#' || rs2.action, rs2.path, '')
				FROM logs l2
				JOIN request_summaries rs2 ON rs2.log_id = l2.id
				WHERE l2.session_id = agg.session_id AND l2.service = agg.service
				ORDER BY l2.timestamp ASC
				LIMIT 1
			), '') AS entry_path,
			COALESCE((
				SELECT COALESCE(rs3.controller || '#' || rs3.action, rs3.path, '')
				FROM logs l3
				JOIN request_summaries rs3 ON rs3.log_id = l3.id
				WHERE l3.session_id = agg.session_id AND l3.service = agg.service
				ORDER BY l3.timestamp DESC
				LIMIT 1
			), '') AS exit_path,
			COALESCE((
				SELECT COALESCE(rs4.status, 0)
				FROM logs l4
				JOIN request_summaries rs4 ON rs4.log_id = l4.id
				WHERE l4.session_id = agg.session_id AND l4.service = agg.service
				ORDER BY l4.timestamp DESC
				LIMIT 1
			), 0) AS exit_status
		FROM (
			SELECT l.session_id,
			       COALESCE(l.user_id, '') as user_id,
			       COALESCE(l.service, '') as service,
			       COALESCE(l.environment, '') as env,
			       MIN(l.timestamp) as started_at,
			       MAX(l.timestamp) as ended_at,
			       COUNT(DISTINCT rs.id) as request_count,
			       SUM(CASE WHEN rs.status >= 500 THEN 1 ELSE 0 END) as error_count,
			       COALESCE(SUM(rs.duration_ms), 0) as total_duration_ms
			FROM logs l
			LEFT JOIN request_summaries rs ON rs.log_id = l.id
			WHERE l.session_id != ''
			  AND l.timestamp >= ?
			GROUP BY l.session_id, l.service
		) agg
	`, sinceStr)
	if err != nil {
		return fmt.Errorf("querying sessions: %w", err)
	}

	type sessionRow struct {
		sessionID, userID, service, env       string
		startedAt, endedAt                    string
		requestCount, errorCount              int
		totalDurationMs                       float64
		entryPath, exitPath                   string
		exitStatus                            int
	}

	var sessions []sessionRow
	for rows.Next() {
		var r sessionRow
		if err := rows.Scan(&r.sessionID, &r.userID, &r.service, &r.env,
			&r.startedAt, &r.endedAt, &r.requestCount, &r.errorCount, &r.totalDurationMs,
			&r.entryPath, &r.exitPath, &r.exitStatus); err != nil {
			rows.Close()
			return fmt.Errorf("scanning session row: %w", err)
		}
		sessions = append(sessions, r)
	}
	rows.Close()

	for _, r := range sessions {
		hasError := 0
		if r.errorCount > 0 {
			hasError = 1
		}

		_, err = s.db.NewRaw(`
			INSERT INTO user_sessions (session_id, user_id, service, environment,
				started_at, ended_at, request_count, error_count, total_duration_ms,
				entry_path, exit_path, exit_status, has_error)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id, service) DO UPDATE SET
				user_id = excluded.user_id,
				environment = excluded.environment,
				ended_at = excluded.ended_at,
				request_count = excluded.request_count,
				error_count = excluded.error_count,
				total_duration_ms = excluded.total_duration_ms,
				entry_path = excluded.entry_path,
				exit_path = excluded.exit_path,
				exit_status = excluded.exit_status,
				has_error = excluded.has_error
		`, r.sessionID, r.userID, r.service, r.env,
			r.startedAt, r.endedAt, r.requestCount, r.errorCount, r.totalDurationMs,
			r.entryPath, r.exitPath, r.exitStatus, hasError,
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("upserting session: %w", err)
		}
	}

	return nil
}

func (s *journeyStore) GetSession(ctx context.Context, sessionID string) (*store.UserSession, error) {
	type row struct {
		ID              int64   `bun:"id"`
		SessionID       string  `bun:"session_id"`
		UserID          string  `bun:"user_id"`
		Service         string  `bun:"service"`
		Environment     string  `bun:"environment"`
		StartedAt       string  `bun:"started_at"`
		EndedAt         string  `bun:"ended_at"`
		RequestCount    int     `bun:"request_count"`
		ErrorCount      int     `bun:"error_count"`
		TotalDurationMs float64 `bun:"total_duration_ms"`
		EntryPath       string  `bun:"entry_path"`
		ExitPath        string  `bun:"exit_path"`
		ExitStatus      int     `bun:"exit_status"`
		HasError        int     `bun:"has_error"`
		CreatedAt       string  `bun:"created_at"`
	}
	var r row
	err := s.db.NewRaw(`
		SELECT id, session_id, user_id, service, environment,
			started_at, ended_at, request_count, error_count, total_duration_ms,
			entry_path, exit_path, exit_status, has_error, created_at
		FROM user_sessions WHERE session_id = ?`, sessionID,
	).Scan(ctx,
		&r.ID, &r.SessionID, &r.UserID, &r.Service, &r.Environment,
		&r.StartedAt, &r.EndedAt, &r.RequestCount, &r.ErrorCount, &r.TotalDurationMs,
		&r.EntryPath, &r.ExitPath, &r.ExitStatus, &r.HasError, &r.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting session: %w", err)
	}
	return &store.UserSession{
		ID: r.ID, SessionID: r.SessionID, UserID: r.UserID,
		Service: r.Service, Environment: r.Environment,
		StartedAt: parseTime(r.StartedAt), EndedAt: parseTime(r.EndedAt),
		RequestCount: r.RequestCount, ErrorCount: r.ErrorCount,
		TotalDurationMs: r.TotalDurationMs,
		EntryPath: r.EntryPath, ExitPath: r.ExitPath,
		ExitStatus: r.ExitStatus, HasError: r.HasError == 1,
		CreatedAt: parseTime(r.CreatedAt),
	}, nil
}

func (s *journeyStore) ListSessions(ctx context.Context, params store.SessionListParams) ([]store.UserSession, error) {
	var conditions []string
	var args []any

	if params.UserID != "" {
		conditions = append(conditions, "user_id = ?")
		args = append(args, params.UserID)
	}
	if params.Service != "" {
		conditions = append(conditions, "service = ?")
		args = append(args, params.Service)
	}
	if params.HasError != nil {
		if *params.HasError {
			conditions = append(conditions, "has_error = 1")
		} else {
			conditions = append(conditions, "has_error = 0")
		}
	}
	if !params.Since.IsZero() {
		conditions = append(conditions, "started_at >= ?")
		args = append(args, params.Since.Format(time.RFC3339))
	}
	if !params.Until.IsZero() {
		conditions = append(conditions, "ended_at <= ?")
		args = append(args, params.Until.Format(time.RFC3339))
	}

	query := `SELECT id, session_id, user_id, service, environment,
		started_at, ended_at, request_count, error_count, total_duration_ms,
		entry_path, exit_path, exit_status, has_error, created_at
		FROM user_sessions`

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY started_at DESC"

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	query += " LIMIT ?"
	args = append(args, limit)

	if params.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, params.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var results []store.UserSession
	for rows.Next() {
		var us store.UserSession
		var startStr, endStr, createdStr string
		var hasError int
		if err := rows.Scan(&us.ID, &us.SessionID, &us.UserID, &us.Service, &us.Environment,
			&startStr, &endStr, &us.RequestCount, &us.ErrorCount, &us.TotalDurationMs,
			&us.EntryPath, &us.ExitPath, &us.ExitStatus, &hasError, &createdStr); err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		us.StartedAt, _ = time.Parse(time.RFC3339, startStr)
		us.EndedAt, _ = time.Parse(time.RFC3339, endStr)
		us.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		us.HasError = hasError == 1
		results = append(results, us)
	}
	return results, rows.Err()
}

func (s *journeyStore) GetSessionRequests(ctx context.Context, sessionID string) ([]store.RequestStep, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.timestamp, COALESCE(rs.controller, ''), COALESCE(rs.action, ''),
		       COALESCE(rs.path, ''), COALESCE(rs.method, ''), COALESCE(rs.status, 0),
		       COALESCE(rs.duration_ms, 0), COALESCE(rs.sql_count, 0),
		       CASE WHEN rs.status >= 500 THEN 1 ELSE 0 END as has_error,
		       COALESCE(l.exception_class, ''),
		       COALESCE(l.request_id, ''), l.id
		FROM logs l
		JOIN request_summaries rs ON rs.log_id = l.id
		WHERE l.session_id = ?
		ORDER BY l.timestamp ASC`,
		sessionID)
	if err != nil {
		return nil, fmt.Errorf("getting session requests: %w", err)
	}
	defer rows.Close()
	return scanRequestSteps(rows)
}

func (s *journeyStore) GetUserJourney(ctx context.Context, userID string, since time.Time, limit int) ([]store.RequestStep, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.timestamp, COALESCE(rs.controller, ''), COALESCE(rs.action, ''),
		       COALESCE(rs.path, ''), COALESCE(rs.method, ''), COALESCE(rs.status, 0),
		       COALESCE(rs.duration_ms, 0), COALESCE(rs.sql_count, 0),
		       CASE WHEN rs.status >= 500 THEN 1 ELSE 0 END as has_error,
		       COALESCE(l.exception_class, ''),
		       COALESCE(l.request_id, ''), l.id
		FROM logs l
		JOIN request_summaries rs ON rs.log_id = l.id
		WHERE l.user_id = ? AND l.timestamp >= ?
		ORDER BY l.timestamp ASC
		LIMIT ?
	`, userID, since.Format(time.RFC3339), limit)
	if err != nil {
		return nil, fmt.Errorf("getting user journey: %w", err)
	}
	defer rows.Close()

	return scanRequestSteps(rows)
}

func scanRequestSteps(rows *sql.Rows) ([]store.RequestStep, error) {
	var results []store.RequestStep
	for rows.Next() {
		var step store.RequestStep
		var tsStr string
		var hasError int
		if err := rows.Scan(&tsStr, &step.Controller, &step.Action,
			&step.Path, &step.Method, &step.Status,
			&step.DurationMs, &step.SQLCount, &hasError,
			&step.ErrorClass, &step.RequestID, &step.LogID); err != nil {
			return nil, fmt.Errorf("scanning request step: %w", err)
		}
		step.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
		step.HasError = hasError == 1
		results = append(results, step)
	}
	return results, rows.Err()
}

func (s *journeyStore) CommonPaths(ctx context.Context, params store.PathAnalysisParams) ([]store.PathFrequency, error) {
	if params.PathLength <= 0 {
		params.PathLength = 5
	}
	if params.MinOccurrences <= 0 {
		params.MinOccurrences = 2
	}

	sinceStr := params.Since.Format(time.RFC3339)

	var sessionConds []string
	var sessionArgs []any
	sessionConds = append(sessionConds, "l.session_id != ''")
	sessionConds = append(sessionConds, "l.timestamp >= ?")
	sessionArgs = append(sessionArgs, sinceStr)

	if params.Service != "" {
		sessionConds = append(sessionConds, "l.service = ?")
		sessionArgs = append(sessionArgs, params.Service)
	}
	if params.ErrorPathsOnly {
		sessionConds = append(sessionConds, "rs.status >= 500")
	}

	query := `
		SELECT l.session_id,
		       COALESCE(rs.controller || '#' || rs.action, rs.path, '') as endpoint,
		       COALESCE(rs.status, 0) as status,
		       COALESCE(rs.duration_ms, 0) as dur
		FROM logs l
		JOIN request_summaries rs ON rs.log_id = l.id
		WHERE ` + strings.Join(sessionConds, " AND ") + `
		ORDER BY l.session_id, l.timestamp ASC
	`

	rows, err := s.db.QueryContext(ctx, query, sessionArgs...)
	if err != nil {
		return nil, fmt.Errorf("querying session paths: %w", err)
	}
	defer rows.Close()

	type reqInfo struct {
		endpoint string
		status   int
		dur      float64
	}
	sessionPaths := make(map[string][]reqInfo)
	for rows.Next() {
		var sid, ep string
		var status int
		var dur float64
		if err := rows.Scan(&sid, &ep, &status, &dur); err != nil {
			return nil, fmt.Errorf("scanning path data: %w", err)
		}
		sessionPaths[sid] = append(sessionPaths[sid], reqInfo{ep, status, dur})
	}

	type pathKey string
	type pathInfo struct {
		steps    []string
		count    int
		totalDur float64
		errCount int
	}
	pathCounts := make(map[pathKey]*pathInfo)

	for _, reqs := range sessionPaths {
		if params.StartingFrom != "" {
			startIdx := -1
			for i, r := range reqs {
				if r.endpoint == params.StartingFrom {
					startIdx = i
					break
				}
			}
			if startIdx < 0 {
				continue
			}
			reqs = reqs[startIdx:]
		}

		n := params.PathLength
		if n > len(reqs) {
			n = len(reqs)
		}
		if n < 2 {
			continue
		}
		steps := make([]string, n)
		var dur float64
		hasErr := false
		for i := 0; i < n; i++ {
			steps[i] = reqs[i].endpoint
			dur += reqs[i].dur
			if reqs[i].status >= 500 {
				hasErr = true
			}
		}

		key := pathKey(strings.Join(steps, " -> "))
		if p, ok := pathCounts[key]; ok {
			p.count++
			p.totalDur += dur
			if hasErr {
				p.errCount++
			}
		} else {
			ec := 0
			if hasErr {
				ec = 1
			}
			pathCounts[key] = &pathInfo{
				steps: steps, count: 1, totalDur: dur, errCount: ec,
			}
		}
	}

	var results []store.PathFrequency
	for _, p := range pathCounts {
		if p.count < params.MinOccurrences {
			continue
		}
		results = append(results, store.PathFrequency{
			Steps:            p.steps,
			Count:            p.count,
			AvgTotalDuration: p.totalDur / float64(p.count),
			ErrorRate:        float64(p.errCount) / float64(p.count),
		})
	}

	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Count > results[i].Count {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	return results, nil
}

func (s *journeyStore) CreateFunnel(ctx context.Context, funnel store.Funnel) (*store.Funnel, error) {
	stepsJSON, err := json.Marshal(funnel.Steps)
	if err != nil {
		return nil, fmt.Errorf("marshaling steps: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	res, err := s.db.NewRaw(`
		INSERT INTO funnels (name, service, steps, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, funnel.Name, funnel.Service, string(stepsJSON), now, now).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating funnel: %w", err)
	}
	id, _ := res.LastInsertId()
	funnel.ID = id
	funnel.CreatedAt, _ = time.Parse(time.RFC3339, now)
	funnel.UpdatedAt = funnel.CreatedAt
	return &funnel, nil
}

func (s *journeyStore) GetFunnel(ctx context.Context, id int64) (*store.Funnel, error) {
	var name, service, stepsJSON, createdAt, updatedAt string
	var funnelID int64
	err := s.db.NewRaw(`
		SELECT id, name, service, steps, created_at, updated_at FROM funnels WHERE id = ?`, id,
	).Scan(ctx, &funnelID, &name, &service, &stepsJSON, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting funnel: %w", err)
	}
	f := &store.Funnel{
		ID: funnelID, Name: name, Service: service,
		CreatedAt: parseTime(createdAt), UpdatedAt: parseTime(updatedAt),
	}
	json.Unmarshal([]byte(stepsJSON), &f.Steps)
	return f, nil
}

func (s *journeyStore) ListFunnels(ctx context.Context) ([]store.Funnel, error) {
	type row struct {
		ID        int64  `bun:"id"`
		Name      string `bun:"name"`
		Service   string `bun:"service"`
		Steps     string `bun:"steps"`
		CreatedAt string `bun:"created_at"`
		UpdatedAt string `bun:"updated_at"`
	}
	var rows []row
	err := s.db.NewRaw(`SELECT id, name, service, steps, created_at, updated_at FROM funnels ORDER BY created_at DESC`).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("listing funnels: %w", err)
	}
	result := make([]store.Funnel, len(rows))
	for i, r := range rows {
		result[i] = store.Funnel{
			ID: r.ID, Name: r.Name, Service: r.Service,
			CreatedAt: parseTime(r.CreatedAt), UpdatedAt: parseTime(r.UpdatedAt),
		}
		json.Unmarshal([]byte(r.Steps), &result[i].Steps)
	}
	return result, nil
}

func (s *journeyStore) AnalyzeFunnel(ctx context.Context, funnelID int64, since time.Time) (*store.FunnelResult, error) {
	funnel, err := s.GetFunnel(ctx, funnelID)
	if err != nil {
		return nil, err
	}
	if len(funnel.Steps) == 0 {
		return &store.FunnelResult{FunnelName: funnel.Name}, nil
	}

	sinceStr := since.Format(time.RFC3339)

	var serviceCond string
	var serviceArgs []any
	if funnel.Service != "" {
		serviceCond = " AND l.service = ?"
		serviceArgs = append(serviceArgs, funnel.Service)
	}

	queryArgs := append([]any{sinceStr}, serviceArgs...)
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.session_id,
		       COALESCE(rs.controller, '') as controller,
		       COALESCE(rs.action, '') as action
		FROM logs l
		JOIN request_summaries rs ON rs.log_id = l.id
		WHERE l.session_id != '' AND l.timestamp >= ?`+serviceCond+`
		ORDER BY l.session_id, l.timestamp ASC
	`, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("querying funnel data: %w", err)
	}
	defer rows.Close()

	sessionActions := make(map[string][]string)
	for rows.Next() {
		var sid, ctrl, action string
		if err := rows.Scan(&sid, &ctrl, &action); err != nil {
			return nil, fmt.Errorf("scanning funnel row: %w", err)
		}
		sessionActions[sid] = append(sessionActions[sid], ctrl+"#"+action)
	}

	stepCounts := make([]int, len(funnel.Steps))
	for _, actions := range sessionActions {
		stepIdx := 0
		target := funnel.Steps[stepIdx].Controller + "#" + funnel.Steps[stepIdx].Action
		for _, a := range actions {
			if a == target {
				stepCounts[stepIdx]++
				stepIdx++
				if stepIdx >= len(funnel.Steps) {
					break
				}
				target = funnel.Steps[stepIdx].Controller + "#" + funnel.Steps[stepIdx].Action
			}
		}
	}

	totalEntered := stepCounts[0]
	var steps []store.FunnelStepResult
	for i, step := range funnel.Steps {
		pct := float64(0)
		if totalEntered > 0 {
			pct = float64(stepCounts[i]) / float64(totalEntered) * 100
		}
		dropOff := 0
		if i > 0 {
			dropOff = stepCounts[i-1] - stepCounts[i]
		}
		label := step.Label
		if label == "" {
			label = step.Controller + "#" + step.Action
		}
		steps = append(steps, store.FunnelStepResult{
			Label: label, Count: stepCounts[i], Pct: round2(pct), DropOff: dropOff,
		})
	}

	overallConversion := float64(0)
	if totalEntered > 0 && len(stepCounts) > 0 {
		overallConversion = float64(stepCounts[len(stepCounts)-1]) / float64(totalEntered)
	}

	return &store.FunnelResult{
		FunnelName: funnel.Name, TotalEntered: totalEntered,
		Steps: steps, OverallConversion: round2(overallConversion),
	}, nil
}

func (s *journeyStore) DeleteFunnel(ctx context.Context, id int64) error {
	res, err := s.db.NewRaw(`DELETE FROM funnels WHERE id = ?`, id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("deleting funnel: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *journeyStore) GetRequestTimeline(ctx context.Context, logID int64) (*store.RequestTimeline, error) {
	var controller, action, path, timeline string
	var durationMs float64
	var rsLogID int64
	err := s.db.NewRaw(`
		SELECT log_id, COALESCE(controller, ''), COALESCE(action, ''),
			COALESCE(path, ''), COALESCE(duration_ms, 0), COALESCE(timeline, '')
		FROM request_summaries WHERE log_id = ?`, logID,
	).Scan(ctx, &rsLogID, &controller, &action, &path, &durationMs, &timeline)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting request timeline: %w", err)
	}
	rt := &store.RequestTimeline{
		LogID: rsLogID, Controller: controller, Action: action,
		Path: path, DurationMs: durationMs,
	}
	if timeline != "" {
		rt.Events = parseTimelineJSON(timeline)
		rt.Bottleneck = findBottleneck(rt.Events)
	}
	return rt, nil
}

func (s *journeyStore) GetSessionTimeline(ctx context.Context, sessionID string) ([]store.RequestTimeline, error) {
	type row struct {
		LogID      int64   `bun:"log_id"`
		Controller string  `bun:"controller"`
		Action     string  `bun:"action"`
		Path       string  `bun:"path"`
		DurationMs float64 `bun:"duration_ms"`
		Timeline   string  `bun:"timeline"`
		Timestamp  string  `bun:"timestamp"`
	}
	var rows []row
	err := s.db.NewRaw(`
		SELECT rs.log_id, COALESCE(rs.controller, '') as controller,
			COALESCE(rs.action, '') as action, COALESCE(rs.path, '') as path,
			COALESCE(rs.duration_ms, 0) as duration_ms, COALESCE(rs.timeline, '') as timeline,
			l.timestamp
		FROM request_summaries rs
		JOIN logs l ON l.id = rs.log_id
		WHERE l.session_id = ?
		ORDER BY l.timestamp ASC`, sessionID,
	).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("getting session timeline: %w", err)
	}

	results := make([]store.RequestTimeline, len(rows))
	for i, r := range rows {
		results[i] = store.RequestTimeline{
			LogID: r.LogID, Controller: r.Controller, Action: r.Action,
			Path: r.Path, DurationMs: r.DurationMs,
			StartedAt: parseTime(r.Timestamp),
		}
		if r.Timeline != "" {
			results[i].Events = parseTimelineJSON(r.Timeline)
			results[i].Bottleneck = findBottleneck(results[i].Events)
		}
	}
	return results, nil
}

// parseTimelineJSON parses the timeline JSON from request_summaries.
func parseTimelineJSON(raw string) []store.TimelineEvent {
	var events []store.TimelineEvent
	if err := json.Unmarshal([]byte(raw), &events); err == nil {
		return events
	}

	var obj struct {
		Events []store.TimelineEvent `json:"events"`
	}
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		return obj.Events
	}

	var generic []map[string]any
	if err := json.Unmarshal([]byte(raw), &generic); err == nil {
		for _, item := range generic {
			ev := store.TimelineEvent{Details: make(map[string]any)}
			if v, ok := item["type"].(string); ok {
				ev.Type = v
			}
			if v, ok := item["name"].(string); ok {
				ev.Name = v
			}
			if v, ok := item["start_ms"].(float64); ok {
				ev.StartMs = v
			}
			if v, ok := item["duration_ms"].(float64); ok {
				ev.DurationMs = v
			}
			for k, v := range item {
				if k != "type" && k != "name" && k != "start_ms" && k != "duration_ms" {
					ev.Details[k] = v
				}
			}
			if len(ev.Details) == 0 {
				ev.Details = nil
			}
			events = append(events, ev)
		}
		return events
	}

	return nil
}

// findBottleneck returns a pointer to the slowest event, or nil if empty.
func findBottleneck(events []store.TimelineEvent) *store.TimelineEvent {
	if len(events) == 0 {
		return nil
	}
	max := 0
	for i := 1; i < len(events); i++ {
		if events[i].DurationMs > events[max].DurationMs {
			max = i
		}
	}
	e := events[max]
	return &e
}

// round2 rounds to 2 decimal places.
func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
