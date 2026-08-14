package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

// activeSessionWindow is how recently a session must have been active to count
// as an active session in Stats.
const activeSessionWindow = 5 * time.Minute

type mcpActivityStore struct {
	db *bun.DB
}

// NewMCPActivityStore creates a new MCPActivityStore backed by SQLite.
func NewMCPActivityStore(db *bun.DB) store.MCPActivityStore {
	return &mcpActivityStore{db: db}
}

func (s *mcpActivityStore) Log(ctx context.Context, params store.LogMCPActivityParams) error {
	eventType := params.EventType
	if eventType == "" {
		eventType = "tool_call"
	}

	isError := int64(0)
	if params.IsError {
		isError = 1
	}

	wasSuggested := int64(0)
	if params.WasSuggested {
		wasSuggested = 1
	}

	var durationMs interface{}
	if params.DurationMs != nil {
		durationMs = *params.DurationMs
	}

	// created_at is written explicitly rather than left to the column default:
	// Stats and Prune compare it lexicographically against RFC3339 cutoffs, so
	// the value has to be RFC3339 UTC and not whatever SQLite's clock renders.
	_, err := s.db.NewRaw(`
		INSERT INTO mcp_activity (
		    session_id, user_id, tool_name, arguments, result_preview,
		    is_error, duration_ms, event_type, environment,
		    investigation_session_id, step_index,
		    was_suggested, suggestion_rank, followed_by, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		params.SessionID, params.UserID, params.ToolName,
		params.Arguments, params.ResultPreview,
		isError, durationMs, eventType, params.Environment,
		params.InvestigationSessionID, params.StepIndex,
		wasSuggested, params.SuggestionRank, "",
		rfc3339(time.Now().UTC()),
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("logging mcp activity: %w", err)
	}

	// Update the previous step's followed_by in the same (sequential) write.
	if params.PreviousStepIndex > 0 && params.InvestigationSessionID != "" {
		_, _ = s.db.NewRaw(`
			UPDATE mcp_activity
			SET followed_by = ?
			WHERE investigation_session_id = ? AND step_index = ?`,
			params.ToolName, params.InvestigationSessionID, params.PreviousStepIndex,
		).Exec(ctx)
	}

	return nil
}

func (s *mcpActivityStore) Stats(ctx context.Context) (*store.MCPActivityStats, error) {
	stats := &store.MCPActivityStats{}

	oneHourAgo := rfc3339(time.Now().UTC().Add(-1 * time.Hour))
	fiveMinAgo := rfc3339(time.Now().UTC().Add(-activeSessionWindow))

	// Active sessions: sessions with activity in the last 5 minutes
	var activeSessions int
	err := s.db.NewRaw(
		"SELECT COUNT(DISTINCT session_id) FROM mcp_activity WHERE created_at > ?",
		fiveMinAgo,
	).Scan(ctx, &activeSessions)
	if err != nil {
		return nil, fmt.Errorf("counting active sessions: %w", err)
	}
	stats.ActiveSessions = activeSessions

	// Calls in last hour
	var callsLastHour int
	err = s.db.NewRaw(
		"SELECT COUNT(*) FROM mcp_activity WHERE event_type = 'tool_call' AND created_at > ?",
		oneHourAgo,
	).Scan(ctx, &callsLastHour)
	if err != nil {
		return nil, fmt.Errorf("counting calls: %w", err)
	}
	stats.CallsLastHour = callsLastHour

	// Errors in last hour
	var errorsLastHour int
	err = s.db.NewRaw(
		"SELECT COUNT(*) FROM mcp_activity WHERE event_type = 'tool_call' AND is_error = 1 AND created_at > ?",
		oneHourAgo,
	).Scan(ctx, &errorsLastHour)
	if err != nil {
		return nil, fmt.Errorf("counting errors: %w", err)
	}
	stats.ErrorsLastHour = errorsLastHour

	// Last activity time
	var lastAtRaw interface{}
	err = s.db.NewRaw("SELECT MAX(created_at) FROM mcp_activity").Scan(ctx, &lastAtRaw)
	if err != nil {
		return nil, fmt.Errorf("querying last activity: %w", err)
	}
	if lastAtRaw != nil {
		if raw, ok := lastAtRaw.(string); ok && raw != "" {
			if t := parseTime(raw); !t.IsZero() {
				stats.LastActivity = &t
			}
		}
	}

	return stats, nil
}

func (s *mcpActivityStore) Recent(ctx context.Context, limit int) ([]store.MCPActivityEvent, error) {
	if limit <= 0 {
		limit = 20
	}

	var events []store.MCPActivityEvent
	err := s.db.NewRaw(`
		SELECT id, session_id, COALESCE(user_id, '') AS user_id, tool_name,
		       COALESCE(arguments, '') AS arguments,
		       COALESCE(result_preview, '') AS result_preview,
		       is_error, duration_ms, event_type, environment, created_at
		FROM mcp_activity
		ORDER BY created_at DESC
		LIMIT ?`, limit,
	).Scan(ctx, &events)
	if err != nil {
		return nil, fmt.Errorf("listing mcp activity: %w", err)
	}
	return events, nil
}

func (s *mcpActivityStore) ListByInvestigationSession(ctx context.Context, sessionID string) ([]store.MCPActivityEvent, error) {
	var events []store.MCPActivityEvent
	err := s.db.NewRaw(`
		SELECT id, session_id, COALESCE(user_id, '') AS user_id, tool_name,
		       COALESCE(arguments, '') AS arguments,
		       COALESCE(result_preview, '') AS result_preview,
		       is_error, duration_ms, event_type, environment, created_at
		FROM mcp_activity
		WHERE investigation_session_id = ?
		ORDER BY step_index ASC, created_at ASC`, sessionID,
	).Scan(ctx, &events)
	if err != nil {
		return nil, fmt.Errorf("listing mcp activity by investigation session: %w", err)
	}
	return events, nil
}

func (s *mcpActivityStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := rfc3339(time.Now().UTC().Add(-olderThan))
	var totalDeleted int64
	for {
		res, err := s.db.NewRaw(
			"DELETE FROM mcp_activity WHERE rowid IN (SELECT m.rowid FROM mcp_activity m WHERE m.created_at < ? LIMIT 1000)",
			cutoff,
		).Exec(ctx)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning mcp activity: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}

func (s *mcpActivityStore) SetSuggestionTracking(ctx context.Context, invSessionID string, stepIndex int, wasSuggested bool, rank int) error {
	wasSuggestedInt := int64(0)
	if wasSuggested {
		wasSuggestedInt = 1
	}
	_, err := s.db.NewRaw(`
		UPDATE mcp_activity
		SET was_suggested = ?, suggestion_rank = ?
		WHERE investigation_session_id = ? AND step_index = ?`,
		wasSuggestedInt, rank, invSessionID, stepIndex,
	).Exec(ctx)
	return err
}

func (s *mcpActivityStore) UpdateFollowedBy(ctx context.Context, invSessionID string, stepIndex int, followedBy string) error {
	_, err := s.db.NewRaw(`
		UPDATE mcp_activity
		SET followed_by = ?
		WHERE investigation_session_id = ? AND step_index = ?`,
		followedBy, invSessionID, stepIndex,
	).Exec(ctx)
	return err
}
