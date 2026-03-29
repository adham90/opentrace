package sqlite

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type workflowTemplateStore struct {
	db *bun.DB
}

// NewWorkflowTemplateStore creates a new WorkflowTemplateStore backed by SQLite.
func NewWorkflowTemplateStore(db *bun.DB) store.WorkflowTemplateStore {
	return &workflowTemplateStore{db: db}
}

// Seed uses a transaction to insert workflow templates, ignoring conflicts.
func (s *workflowTemplateStore) Seed(ctx context.Context, templates []store.WorkflowTemplate) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, t := range templates {
			source := t.Source
			if source == "" {
				source = "curated"
			}
			argsHint := t.ArgsHint
			if argsHint == "" {
				argsHint = "{}"
			}
			_, err := tx.NewRaw(
				`INSERT OR IGNORE INTO workflow_templates (intent, name, step_order, tool_name, args_hint, source, resolved_session_count)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				t.Intent, t.Name, t.StepOrder, t.ToolName, argsHint, source, t.ResolvedSessionCount,
			).Exec(ctx)
			if err != nil {
				return fmt.Errorf("seeding workflow template %s step %d: %w", t.Name, t.StepOrder, err)
			}
		}
		return nil
	})
}

func (s *workflowTemplateStore) GetNextStep(ctx context.Context, intent string, stepOrder int) ([]store.WorkflowTemplate, error) {
	var templates []store.WorkflowTemplate
	err := s.db.NewSelect().Model(&templates).
		Where("intent = ?", intent).
		Where("step_order = ?", stepOrder).
		OrderExpr("resolved_session_count DESC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying next workflow step: %w", err)
	}
	return templates, nil
}

func (s *workflowTemplateStore) GetByName(ctx context.Context, name string) ([]store.WorkflowTemplate, error) {
	var templates []store.WorkflowTemplate
	err := s.db.NewSelect().Model(&templates).
		Where("name = ?", name).
		OrderExpr("step_order").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying workflow by name: %w", err)
	}
	return templates, nil
}

func (s *workflowTemplateStore) List(ctx context.Context, intent string) ([]store.WorkflowTemplate, error) {
	var templates []store.WorkflowTemplate
	err := s.db.NewSelect().Model(&templates).
		Where("intent = ?", intent).
		OrderExpr("name, step_order").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing workflow templates: %w", err)
	}
	return templates, nil
}
