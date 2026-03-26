package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/adham90/opentrace/pkg/store"
)

type workflowTemplateStore struct {
	db *sql.DB
	q  *Queries
}

// NewWorkflowTemplateStore creates a new WorkflowTemplateStore backed by SQLite.
func NewWorkflowTemplateStore(db *sql.DB) store.WorkflowTemplateStore {
	return &workflowTemplateStore{db: db, q: New(db)}
}

// Seed uses hand-written SQL with a transaction because there is no sqlc-generated equivalent.
func (s *workflowTemplateStore) Seed(ctx context.Context, templates []store.WorkflowTemplate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning seed transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO workflow_templates (intent, name, step_order, tool_name, args_hint, source, resolved_session_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing seed statement: %w", err)
	}
	defer stmt.Close()

	for _, t := range templates {
		source := t.Source
		if source == "" {
			source = "curated"
		}
		argsHint := t.ArgsHint
		if argsHint == "" {
			argsHint = "{}"
		}
		if _, err := stmt.ExecContext(ctx, t.Intent, t.Name, t.StepOrder, t.ToolName, argsHint, source, t.ResolvedSessionCount); err != nil {
			return fmt.Errorf("seeding workflow template %s step %d: %w", t.Name, t.StepOrder, err)
		}
	}

	return tx.Commit()
}

func (s *workflowTemplateStore) GetNextStep(ctx context.Context, intent string, stepOrder int) ([]store.WorkflowTemplate, error) {
	rows, err := s.q.GetWorkflowNextStep(ctx, GetWorkflowNextStepParams{
		Intent:    intent,
		StepOrder: int64(stepOrder),
	})
	if err != nil {
		return nil, fmt.Errorf("querying next workflow step: %w", err)
	}
	return toStoreWorkflowTemplates(rows), nil
}

func (s *workflowTemplateStore) GetByName(ctx context.Context, name string) ([]store.WorkflowTemplate, error) {
	rows, err := s.q.GetWorkflowByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("querying workflow by name: %w", err)
	}
	return toStoreWorkflowTemplates(rows), nil
}

func (s *workflowTemplateStore) List(ctx context.Context, intent string) ([]store.WorkflowTemplate, error) {
	rows, err := s.q.ListWorkflowTemplates(ctx, intent)
	if err != nil {
		return nil, fmt.Errorf("listing workflow templates: %w", err)
	}
	return toStoreWorkflowTemplates(rows), nil
}
