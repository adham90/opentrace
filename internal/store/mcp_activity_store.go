package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type mcpActivityStore struct {
	db *sql.DB
}

// NewMCPActivityStore creates a new MCPActivityStore backed by SQLite.
func NewMCPActivityStore(db *sql.DB) MCPActivityStore {
	return &mcpActivityStore{db: db}
}

func (s *mcpActivityStore) Log(ctx context.Context, params LogMCPActivityParams) error {
	eventType := params.EventType
	if eventType == "" {
		eventType = "tool_call"
	}

	var isError int
	if params.IsError {
		isError = 1
	}
	var wasSuggested int
	if params.WasSuggested {
		wasSuggested = 1
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO mcp_activity (session_id, user_id, tool_name, arguments, result_preview, is_error, duration_ms, event_type, investigation_session_id, step_index, was_suggested, suggestion_rank, followed_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		params.SessionID, params.UserID, params.ToolName,
		params.Arguments, params.ResultPreview,
		isError, params.DurationMs, eventType,
		params.InvestigationSessionID, params.StepIndex,
		wasSuggested, params.SuggestionRank, params.FollowedBy,
	)
	if err != nil {
		return fmt.Errorf("logging mcp activity: %w", err)
	}
	return nil
}

func (s *mcpActivityStore) Stats(ctx context.Context) (*MCPActivityStats, error) {
	stats := &MCPActivityStats{}

	oneHourAgo := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	fiveMinAgo := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)

	// Active sessions: sessions with activity in the last 5 minutes
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT session_id) FROM mcp_activity WHERE created_at > ?`,
		fiveMinAgo,
	).Scan(&stats.ActiveSessions)
	if err != nil {
		return nil, fmt.Errorf("counting active sessions: %w", err)
	}

	// Calls in last hour
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mcp_activity WHERE event_type = 'tool_call' AND created_at > ?`,
		oneHourAgo,
	).Scan(&stats.CallsLastHour)
	if err != nil {
		return nil, fmt.Errorf("counting calls: %w", err)
	}

	// Errors in last hour
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mcp_activity WHERE event_type = 'tool_call' AND is_error = 1 AND created_at > ?`,
		oneHourAgo,
	).Scan(&stats.ErrorsLastHour)
	if err != nil {
		return nil, fmt.Errorf("counting errors: %w", err)
	}

	// Last activity time
	var lastAt sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT MAX(created_at) FROM mcp_activity`,
	).Scan(&lastAt)
	if err != nil {
		return nil, fmt.Errorf("querying last activity: %w", err)
	}
	if lastAt.Valid {
		t, _ := time.Parse(time.RFC3339, lastAt.String)
		if !t.IsZero() {
			stats.LastActivity = &t
		}
	}

	return stats, nil
}

func (s *mcpActivityStore) Recent(ctx context.Context, limit int) ([]MCPActivityEvent, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, COALESCE(user_id, ''), tool_name, COALESCE(arguments, ''),
		        COALESCE(result_preview, ''), is_error, duration_ms, event_type, created_at
		 FROM mcp_activity
		 ORDER BY created_at DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing mcp activity: %w", err)
	}
	defer rows.Close()

	var result []MCPActivityEvent
	for rows.Next() {
		var e MCPActivityEvent
		var isError int
		var durationMs sql.NullInt64
		var createdAt string

		if err := rows.Scan(
			&e.ID, &e.SessionID, &e.UserID, &e.ToolName,
			&e.Arguments, &e.ResultPreview,
			&isError, &durationMs, &e.EventType, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning mcp activity: %w", err)
		}

		e.IsError = isError != 0
		if durationMs.Valid {
			e.DurationMs = &durationMs.Int64
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)

		result = append(result, e)
	}

	return result, rows.Err()
}

func (s *mcpActivityStore) ListByInvestigationSession(ctx context.Context, sessionID string) ([]MCPActivityEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, COALESCE(user_id, ''), tool_name, COALESCE(arguments, ''),
		        COALESCE(result_preview, ''), is_error, duration_ms, event_type, created_at
		 FROM mcp_activity
		 WHERE investigation_session_id = ?
		 ORDER BY step_index ASC, created_at ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing mcp activity by investigation session: %w", err)
	}
	defer rows.Close()

	var result []MCPActivityEvent
	for rows.Next() {
		var e MCPActivityEvent
		var isError int
		var durationMs sql.NullInt64
		var createdAt string

		if err := rows.Scan(
			&e.ID, &e.SessionID, &e.UserID, &e.ToolName,
			&e.Arguments, &e.ResultPreview,
			&isError, &durationMs, &e.EventType, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning mcp activity: %w", err)
		}

		e.IsError = isError != 0
		if durationMs.Valid {
			e.DurationMs = &durationMs.Int64
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)

		result = append(result, e)
	}

	return result, rows.Err()
}

func (s *mcpActivityStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		result, err := s.db.ExecContext(ctx,
			`DELETE FROM mcp_activity WHERE rowid IN (SELECT rowid FROM mcp_activity WHERE created_at < ? LIMIT 1000)`, cutoff,
		)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning mcp activity: %w", err)
		}
		n, _ := result.RowsAffected()
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}

func (s *mcpActivityStore) SetSuggestionTracking(ctx context.Context, invSessionID string, stepIndex int, wasSuggested bool, rank int) error {
	var suggested int
	if wasSuggested {
		suggested = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE mcp_activity SET was_suggested = ?, suggestion_rank = ? WHERE investigation_session_id = ? AND step_index = ?`,
		suggested, rank, invSessionID, stepIndex,
	)
	if err != nil {
		return fmt.Errorf("setting suggestion tracking: %w", err)
	}
	return nil
}

func (s *mcpActivityStore) UpdateFollowedBy(ctx context.Context, invSessionID string, stepIndex int, followedBy string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE mcp_activity SET followed_by = ? WHERE investigation_session_id = ? AND step_index = ?`,
		followedBy, invSessionID, stepIndex,
	)
	if err != nil {
		return fmt.Errorf("updating followed_by: %w", err)
	}
	return nil
}
