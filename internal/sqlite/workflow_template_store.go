package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type workflowTemplateStore struct {
	db *sql.DB
}

// NewWorkflowTemplateStore creates a new WorkflowTemplateStore backed by SQLite.
func NewWorkflowTemplateStore(db *sql.DB) store.WorkflowTemplateStore {
	return &workflowTemplateStore{db: db}
}

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
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, intent, name, step_order, tool_name, args_hint, source, resolved_session_count, created_at
		 FROM workflow_templates
		 WHERE intent = ? AND step_order = ?
		 ORDER BY resolved_session_count DESC`,
		intent, stepOrder,
	)
	if err != nil {
		return nil, fmt.Errorf("querying next workflow step: %w", err)
	}
	defer rows.Close()

	return scanTemplates(rows)
}

func (s *workflowTemplateStore) GetByName(ctx context.Context, name string) ([]store.WorkflowTemplate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, intent, name, step_order, tool_name, args_hint, source, resolved_session_count, created_at
		 FROM workflow_templates
		 WHERE name = ?
		 ORDER BY step_order`,
		name,
	)
	if err != nil {
		return nil, fmt.Errorf("querying workflow by name: %w", err)
	}
	defer rows.Close()

	return scanTemplates(rows)
}

func (s *workflowTemplateStore) List(ctx context.Context, intent string) ([]store.WorkflowTemplate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, intent, name, step_order, tool_name, args_hint, source, resolved_session_count, created_at
		 FROM workflow_templates
		 WHERE intent = ?
		 ORDER BY name, step_order`,
		intent,
	)
	if err != nil {
		return nil, fmt.Errorf("listing workflow templates: %w", err)
	}
	defer rows.Close()

	return scanTemplates(rows)
}

func scanTemplates(rows *sql.Rows) ([]store.WorkflowTemplate, error) {
	var result []store.WorkflowTemplate
	for rows.Next() {
		var t store.WorkflowTemplate
		var createdAt string
		if err := rows.Scan(&t.ID, &t.Intent, &t.Name, &t.StepOrder, &t.ToolName, &t.ArgsHint, &t.Source, &t.ResolvedSessionCount, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning workflow template: %w", err)
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		result = append(result, t)
	}
	return result, rows.Err()
}
