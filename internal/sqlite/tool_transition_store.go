package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type toolTransitionStore struct {
	db *sql.DB
}

// NewToolTransitionStore creates a new ToolTransitionStore backed by SQLite.
func NewToolTransitionStore(db *sql.DB) store.ToolTransitionStore {
	return &toolTransitionStore{db: db}
}

func (s *toolTransitionStore) Increment(ctx context.Context, fromTool, toTool, intent string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tool_transitions (from_tool, to_tool, intent, total_count, last_seen_at)
		 VALUES (?, ?, ?, 1, datetime('now'))
		 ON CONFLICT(from_tool, to_tool, intent) DO UPDATE SET
		   total_count = total_count + 1,
		   last_seen_at = datetime('now')`,
		fromTool, toTool, intent,
	)
	if err != nil {
		return fmt.Errorf("incrementing tool transition: %w", err)
	}
	return nil
}

func (s *toolTransitionStore) IncrementWithOutcome(ctx context.Context, fromTool, toTool, intent, outcome string) error {
	var resolvedInc, abandonedInc int
	switch outcome {
	case string(store.InvestigationStatusResolved):
		resolvedInc = 1
	case string(store.InvestigationStatusAbandoned):
		abandonedInc = 1
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tool_transitions (from_tool, to_tool, intent, total_count, resolved_count, abandoned_count, last_seen_at)
		 VALUES (?, ?, ?, 1, ?, ?, datetime('now'))
		 ON CONFLICT(from_tool, to_tool, intent) DO UPDATE SET
		   total_count = total_count + 1,
		   resolved_count = resolved_count + ?,
		   abandoned_count = abandoned_count + ?,
		   last_seen_at = datetime('now')`,
		fromTool, toTool, intent, resolvedInc, abandonedInc,
		resolvedInc, abandonedInc,
	)
	if err != nil {
		return fmt.Errorf("incrementing tool transition with outcome: %w", err)
	}
	return nil
}

func (s *toolTransitionStore) GetTransitions(ctx context.Context, params store.GetTransitionsParams) ([]store.ToolTransition, error) {
	minSupport := params.MinSupport
	if minSupport <= 0 {
		minSupport = 1
	}
	maxAgeDays := params.MaxAgeDays
	if maxAgeDays <= 0 {
		maxAgeDays = 90
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -maxAgeDays).Format(time.RFC3339)

	rows, err := s.db.QueryContext(ctx,
		`SELECT from_tool, to_tool, intent, total_count, resolved_count, abandoned_count, avg_duration_ms, last_seen_at
		 FROM tool_transitions
		 WHERE from_tool = ?
		   AND (intent = ? OR intent = '')
		   AND total_count >= ?
		   AND last_seen_at >= ?
		 ORDER BY resolved_count DESC, total_count DESC
		 LIMIT 20`,
		params.FromTool, params.Intent, minSupport, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("querying tool transitions: %w", err)
	}
	defer rows.Close()

	var result []store.ToolTransition
	for rows.Next() {
		var t store.ToolTransition
		var lastSeenAt string
		if err := rows.Scan(&t.FromTool, &t.ToTool, &t.Intent, &t.TotalCount, &t.ResolvedCount, &t.AbandonedCount, &t.AvgDurationMs, &lastSeenAt); err != nil {
			return nil, fmt.Errorf("scanning tool transition: %w", err)
		}
		t.LastSeenAt, _ = time.Parse(time.RFC3339, lastSeenAt)
		result = append(result, t)
	}
	return result, rows.Err()
}

func (s *toolTransitionStore) GetDeadEnds(ctx context.Context, intent string) ([]store.ToolTransition, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT from_tool, to_tool, intent, total_count, resolved_count, abandoned_count, avg_duration_ms, last_seen_at
		 FROM tool_transitions
		 WHERE abandoned_count > resolved_count
		   AND total_count >= 3
		   AND (intent = ? OR intent = '')
		 ORDER BY abandoned_count DESC
		 LIMIT 10`,
		intent,
	)
	if err != nil {
		return nil, fmt.Errorf("querying dead ends: %w", err)
	}
	defer rows.Close()

	var result []store.ToolTransition
	for rows.Next() {
		var t store.ToolTransition
		var lastSeenAt string
		if err := rows.Scan(&t.FromTool, &t.ToTool, &t.Intent, &t.TotalCount, &t.ResolvedCount, &t.AbandonedCount, &t.AvgDurationMs, &lastSeenAt); err != nil {
			return nil, fmt.Errorf("scanning dead end transition: %w", err)
		}
		t.LastSeenAt, _ = time.Parse(time.RFC3339, lastSeenAt)
		result = append(result, t)
	}
	return result, rows.Err()
}

func (s *toolTransitionStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM tool_transitions WHERE last_seen_at < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("pruning tool transitions: %w", err)
	}
	return result.RowsAffected()
}
