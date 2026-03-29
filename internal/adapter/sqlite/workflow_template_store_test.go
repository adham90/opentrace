package sqlite

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/pkg/store"
)

func TestWorkflowTemplateStore_SeedIdempotent(t *testing.T) {
	db := setupTestDB(t)
	s := NewWorkflowTemplateStore(db)
	ctx := context.Background()

	templates := []store.WorkflowTemplate{
		{Intent: "investigation", Name: "error_spike", StepOrder: 0, ToolName: "diagnose", Source: "curated"},
		{Intent: "investigation", Name: "error_spike", StepOrder: 1, ToolName: "error_groups", Source: "curated"},
	}

	// Seed twice — should not create duplicates
	if err := s.Seed(ctx, templates); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if err := s.Seed(ctx, templates); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	all, err := s.List(ctx, "investigation")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 templates after double seed, got %d", len(all))
	}
}

func TestWorkflowTemplateStore_GetNextStep(t *testing.T) {
	db := setupTestDB(t)
	s := NewWorkflowTemplateStore(db)
	ctx := context.Background()

	templates := []store.WorkflowTemplate{
		{Intent: "investigation", Name: "error_spike", StepOrder: 0, ToolName: "diagnose", Source: "curated"},
		{Intent: "investigation", Name: "error_spike", StepOrder: 1, ToolName: "error_groups", Source: "curated"},
		{Intent: "investigation", Name: "slow_db", StepOrder: 0, ToolName: "db_query_stats", Source: "curated"},
	}

	if err := s.Seed(ctx, templates); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Get step 0 for investigation intent
	step0, err := s.GetNextStep(ctx, "investigation", 0)
	if err != nil {
		t.Fatalf("get next step: %v", err)
	}
	if len(step0) != 2 {
		t.Fatalf("expected 2 step-0 templates, got %d", len(step0))
	}

	// Get step 1 — only error_spike has a step 1
	step1, err := s.GetNextStep(ctx, "investigation", 1)
	if err != nil {
		t.Fatalf("get next step 1: %v", err)
	}
	if len(step1) != 1 {
		t.Fatalf("expected 1 step-1 template, got %d", len(step1))
	}
	if step1[0].ToolName != "error_groups" {
		t.Errorf("expected error_groups, got %q", step1[0].ToolName)
	}
}

func TestWorkflowTemplateStore_GetByName(t *testing.T) {
	db := setupTestDB(t)
	s := NewWorkflowTemplateStore(db)
	ctx := context.Background()

	templates := []store.WorkflowTemplate{
		{Intent: "investigation", Name: "error_spike", StepOrder: 0, ToolName: "diagnose", Source: "curated"},
		{Intent: "investigation", Name: "error_spike", StepOrder: 1, ToolName: "error_groups", Source: "curated"},
		{Intent: "investigation", Name: "error_spike", StepOrder: 2, ToolName: "error_detail", Source: "curated"},
	}

	if err := s.Seed(ctx, templates); err != nil {
		t.Fatalf("seed: %v", err)
	}

	steps, err := s.GetByName(ctx, "error_spike")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	// Verify order
	for i, step := range steps {
		if step.StepOrder != i {
			t.Errorf("step %d: expected step_order=%d, got %d", i, i, step.StepOrder)
		}
	}
}
