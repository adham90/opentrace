package store

import (
	"context"
	"time"
)

// ToolTransitionStore tracks tool-to-tool transitions for ranking suggestions.
type ToolTransitionStore interface {
	Increment(ctx context.Context, fromTool, toTool, intent string) error
	IncrementWithOutcome(ctx context.Context, fromTool, toTool, intent, outcome string) error
	GetTransitions(ctx context.Context, params GetTransitionsParams) ([]ToolTransition, error)
	GetDeadEnds(ctx context.Context, intent string) ([]ToolTransition, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// WorkflowTemplateStore manages curated and learned workflow templates.
type WorkflowTemplateStore interface {
	Seed(ctx context.Context, templates []WorkflowTemplate) error
	GetNextStep(ctx context.Context, intent string, stepOrder int) ([]WorkflowTemplate, error)
	GetByName(ctx context.Context, name string) ([]WorkflowTemplate, error)
	List(ctx context.Context, intent string) ([]WorkflowTemplate, error)
}
