package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type runbookEffectivenessStore struct {
	db *sql.DB
}

// NewRunbookEffectivenessStore creates a new RunbookEffectivenessStore backed by SQLite.
func NewRunbookEffectivenessStore(db *sql.DB) RunbookEffectivenessStore {
	return &runbookEffectivenessStore{db: db}
}

func (s *runbookEffectivenessStore) RecordExecution(ctx context.Context, runbookName string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runbook_effectiveness (runbook_name, total_executions, last_executed_at)
		 VALUES (?, 1, ?)
		 ON CONFLICT(runbook_name) DO UPDATE SET
		     total_executions = total_executions + 1,
		     last_executed_at = ?`,
		runbookName, now, now,
	)
	if err != nil {
		return fmt.Errorf("recording runbook execution: %w", err)
	}
	return nil
}

func (s *runbookEffectivenessStore) UpdateOutcome(ctx context.Context, params UpdateRunbookEffectivenessParams) error {
	var col string
	switch params.Outcome {
	case "resolved":
		col = "resolved_sessions"
	case "abandoned":
		col = "abandoned_sessions"
	default:
		return nil // ignore unknown outcomes
	}

	// Update outcome count and recalculate running averages.
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE runbook_effectiveness SET
		    %s = %s + 1,
		    avg_steps_after = (avg_steps_after * (%s) + ?) / (%s + 1),
		    avg_session_duration_seconds = (avg_session_duration_seconds * (%s) + ?) / (%s + 1)
		 WHERE runbook_name = ?`,
			col, col, col, col, col, col),
		params.StepsAfter, params.DurationSec, params.RunbookName,
	)
	if err != nil {
		return fmt.Errorf("updating runbook outcome: %w", err)
	}
	return nil
}

func (s *runbookEffectivenessStore) GetMostEffective(ctx context.Context) (*RunbookEffectiveness, error) {
	var re RunbookEffectiveness
	var lastExec string

	err := s.db.QueryRowContext(ctx,
		`SELECT runbook_name, total_executions, resolved_sessions, abandoned_sessions,
		        avg_steps_after, avg_session_duration_seconds, last_executed_at
		 FROM runbook_effectiveness
		 WHERE total_executions >= 3 AND resolved_sessions > abandoned_sessions
		 ORDER BY CAST(resolved_sessions AS REAL) / total_executions DESC
		 LIMIT 1`,
	).Scan(
		&re.RunbookName, &re.TotalExecutions, &re.ResolvedSessions, &re.AbandonedSessions,
		&re.AvgStepsAfter, &re.AvgSessionDurationSeconds, &lastExec,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting most effective runbook: %w", err)
	}

	re.LastExecutedAt, _ = time.Parse(time.RFC3339, lastExec)
	return &re, nil
}

func (s *runbookEffectivenessStore) List(ctx context.Context) ([]RunbookEffectiveness, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT runbook_name, total_executions, resolved_sessions, abandoned_sessions,
		        avg_steps_after, avg_session_duration_seconds, last_executed_at
		 FROM runbook_effectiveness
		 ORDER BY total_executions DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing runbook effectiveness: %w", err)
	}
	defer rows.Close()

	var result []RunbookEffectiveness
	for rows.Next() {
		var re RunbookEffectiveness
		var lastExec string
		if err := rows.Scan(
			&re.RunbookName, &re.TotalExecutions, &re.ResolvedSessions, &re.AbandonedSessions,
			&re.AvgStepsAfter, &re.AvgSessionDurationSeconds, &lastExec,
		); err != nil {
			return nil, fmt.Errorf("scanning runbook effectiveness: %w", err)
		}
		re.LastExecutedAt, _ = time.Parse(time.RFC3339, lastExec)
		result = append(result, re)
	}
	return result, rows.Err()
}
