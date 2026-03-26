package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type mcpActivityStore struct {
	db *sql.DB
	q  *Queries
}

// NewMCPActivityStore creates a new MCPActivityStore backed by SQLite.
func NewMCPActivityStore(db *sql.DB) store.MCPActivityStore {
	return &mcpActivityStore{db: db, q: New(db)}
}

func (s *mcpActivityStore) Log(ctx context.Context, params store.LogMCPActivityParams) error {
	eventType := params.EventType
	if eventType == "" {
		eventType = "tool_call"
	}

	err := s.q.InsertMCPActivity(ctx, InsertMCPActivityParams{
		SessionID:              params.SessionID,
		UserID:                 nullString(params.UserID),
		ToolName:               params.ToolName,
		Arguments:              nullString(params.Arguments),
		ResultPreview:          nullString(params.ResultPreview),
		IsError:                boolToInt64(params.IsError),
		DurationMs:             nullInt64(params.DurationMs),
		EventType:              eventType,
		InvestigationSessionID: params.InvestigationSessionID,
		StepIndex:              int64(params.StepIndex),
		WasSuggested:           boolToInt64(params.WasSuggested),
		SuggestionRank:         int64(params.SuggestionRank),
		FollowedBy:             "",
	})
	if err != nil {
		return fmt.Errorf("logging mcp activity: %w", err)
	}

	// Update the previous step's followed_by in the same (sequential) write.
	if params.PreviousStepIndex > 0 && params.InvestigationSessionID != "" {
		_ = s.q.UpdateFollowedBy(ctx, UpdateFollowedByParams{
			FollowedBy:             params.ToolName,
			InvestigationSessionID: params.InvestigationSessionID,
			StepIndex:              int64(params.PreviousStepIndex),
		})
	}

	return nil
}

func (s *mcpActivityStore) Stats(ctx context.Context) (*store.MCPActivityStats, error) {
	stats := &store.MCPActivityStats{}

	oneHourAgo := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	fiveMinAgo := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)

	// Active sessions: sessions with activity in the last 5 minutes
	activeSessions, err := s.q.GetMCPActiveSessionCount(ctx, fiveMinAgo)
	if err != nil {
		return nil, fmt.Errorf("counting active sessions: %w", err)
	}
	stats.ActiveSessions = int(activeSessions)

	// Calls in last hour
	callsLastHour, err := s.q.GetMCPCallCountSince(ctx, oneHourAgo)
	if err != nil {
		return nil, fmt.Errorf("counting calls: %w", err)
	}
	stats.CallsLastHour = int(callsLastHour)

	// Errors in last hour
	errorsLastHour, err := s.q.GetMCPErrorCountSince(ctx, oneHourAgo)
	if err != nil {
		return nil, fmt.Errorf("counting errors: %w", err)
	}
	stats.ErrorsLastHour = int(errorsLastHour)

	// Last activity time
	lastAtRaw, err := s.q.GetMCPLastActivityTime(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying last activity: %w", err)
	}
	if lastAtRaw != nil {
		if s, ok := lastAtRaw.(string); ok && s != "" {
			t, _ := time.Parse(time.RFC3339, s)
			if !t.IsZero() {
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

	rows, err := s.q.ListRecentMCPActivity(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("listing mcp activity: %w", err)
	}
	return toStoreMCPActivityEvents(rows), nil
}

func (s *mcpActivityStore) ListByInvestigationSession(ctx context.Context, sessionID string) ([]store.MCPActivityEvent, error) {
	rows, err := s.q.ListMCPActivityByInvestigationSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("listing mcp activity by investigation session: %w", err)
	}
	return toStoreMCPActivityEventsFromInvRows(rows), nil
}

func (s *mcpActivityStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		n, err := s.q.PruneMCPActivity(ctx, cutoff)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning mcp activity: %w", err)
		}
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}

func (s *mcpActivityStore) SetSuggestionTracking(ctx context.Context, invSessionID string, stepIndex int, wasSuggested bool, rank int) error {
	return s.q.SetSuggestionTracking(ctx, SetSuggestionTrackingParams{
		WasSuggested:           boolToInt64(wasSuggested),
		SuggestionRank:         int64(rank),
		InvestigationSessionID: invSessionID,
		StepIndex:              int64(stepIndex),
	})
}

func (s *mcpActivityStore) UpdateFollowedBy(ctx context.Context, invSessionID string, stepIndex int, followedBy string) error {
	return s.q.UpdateFollowedBy(ctx, UpdateFollowedByParams{
		FollowedBy:             followedBy,
		InvestigationSessionID: invSessionID,
		StepIndex:              int64(stepIndex),
	})
}
