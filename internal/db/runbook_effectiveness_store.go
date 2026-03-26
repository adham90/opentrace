package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type runbookEffectivenessStore struct {
	db *sql.DB
	q  *Queries
}

// NewRunbookEffectivenessStore creates a new RunbookEffectivenessStore backed by SQLite.
func NewRunbookEffectivenessStore(db *sql.DB) store.RunbookEffectivenessStore {
	return &runbookEffectivenessStore{db: db, q: New(db)}
}

// RecordExecution uses hand-written SQL because the sqlc-generated query
// has unexpanded sqlc.arg() references in the ON CONFLICT clause.
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

func (s *runbookEffectivenessStore) UpdateOutcome(ctx context.Context, params store.UpdateRunbookEffectivenessParams) error {
	switch params.Outcome {
	case "resolved":
		err := s.q.UpdateRunbookOutcomeResolved(ctx, UpdateRunbookOutcomeResolvedParams{
			StepsAfter:  int64(params.StepsAfter),
			DurationSec: int64(params.DurationSec),
			RunbookName: params.RunbookName,
		})
		if err != nil {
			return fmt.Errorf("updating runbook outcome: %w", err)
		}
		return nil
	case "abandoned":
		err := s.q.UpdateRunbookOutcomeAbandoned(ctx, UpdateRunbookOutcomeAbandonedParams{
			StepsAfter:  int64(params.StepsAfter),
			DurationSec: int64(params.DurationSec),
			RunbookName: params.RunbookName,
		})
		if err != nil {
			return fmt.Errorf("updating runbook outcome: %w", err)
		}
		return nil
	default:
		return nil // ignore unknown outcomes
	}
}

func (s *runbookEffectivenessStore) GetMostEffective(ctx context.Context) (*store.RunbookEffectiveness, error) {
	row, err := s.q.GetMostEffectiveRunbook(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting most effective runbook: %w", err)
	}

	result := toStoreRunbookEffectiveness(row)
	return &result, nil
}

func (s *runbookEffectivenessStore) List(ctx context.Context) ([]store.RunbookEffectiveness, error) {
	rows, err := s.q.ListRunbookEffectiveness(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing runbook effectiveness: %w", err)
	}
	return toStoreRunbookEffectivenessList(rows), nil
}
