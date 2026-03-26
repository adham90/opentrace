package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type runbookEffectivenessStore struct {
	db *bun.DB
}

// NewRunbookEffectivenessStore creates a new RunbookEffectivenessStore backed by SQLite.
func NewRunbookEffectivenessStore(db *bun.DB) store.RunbookEffectivenessStore {
	return &runbookEffectivenessStore{db: db}
}

// RecordExecution uses raw SQL for the upsert with increment logic.
func (s *runbookEffectivenessStore) RecordExecution(ctx context.Context, runbookName string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.NewRaw(
		`INSERT INTO runbook_effectiveness (runbook_name, total_executions, last_executed_at)
		 VALUES (?, 1, ?)
		 ON CONFLICT(runbook_name) DO UPDATE SET
		     total_executions = total_executions + 1,
		     last_executed_at = ?`,
		runbookName, now, now,
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("recording runbook execution: %w", err)
	}
	return nil
}

func (s *runbookEffectivenessStore) UpdateOutcome(ctx context.Context, params store.UpdateRunbookEffectivenessParams) error {
	switch params.Outcome {
	case "resolved":
		_, err := s.db.NewRaw(
			`UPDATE runbook_effectiveness SET
			    resolved_sessions = resolved_sessions + 1,
			    avg_steps_after = (avg_steps_after * resolved_sessions + ?) / (resolved_sessions + 1),
			    avg_session_duration_seconds = (avg_session_duration_seconds * resolved_sessions + ?) / (resolved_sessions + 1)
			WHERE runbook_name = ?`,
			params.StepsAfter, params.DurationSec, params.RunbookName,
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("updating runbook outcome: %w", err)
		}
		return nil
	case "abandoned":
		_, err := s.db.NewRaw(
			`UPDATE runbook_effectiveness SET
			    abandoned_sessions = abandoned_sessions + 1,
			    avg_steps_after = (avg_steps_after * abandoned_sessions + ?) / (abandoned_sessions + 1),
			    avg_session_duration_seconds = (avg_session_duration_seconds * abandoned_sessions + ?) / (abandoned_sessions + 1)
			WHERE runbook_name = ?`,
			params.StepsAfter, params.DurationSec, params.RunbookName,
		).Exec(ctx)
		if err != nil {
			return fmt.Errorf("updating runbook outcome: %w", err)
		}
		return nil
	default:
		return nil // ignore unknown outcomes
	}
}

func (s *runbookEffectivenessStore) GetMostEffective(ctx context.Context) (*store.RunbookEffectiveness, error) {
	var result store.RunbookEffectiveness
	err := s.db.NewSelect().Model(&result).
		Where("total_executions >= 3").
		Where("resolved_sessions > abandoned_sessions").
		OrderExpr("CAST(resolved_sessions AS REAL) / total_executions DESC").
		Limit(1).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting most effective runbook: %w", err)
	}
	return &result, nil
}

func (s *runbookEffectivenessStore) List(ctx context.Context) ([]store.RunbookEffectiveness, error) {
	var results []store.RunbookEffectiveness
	err := s.db.NewSelect().Model(&results).
		OrderExpr("total_executions DESC").
		Limit(100).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing runbook effectiveness: %w", err)
	}
	return results, nil
}
