package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// mockToolTransitionStore for ranking tests.
type mockToolTransitionStore struct {
	transitions []store.ToolTransition
	deadEnds    []store.ToolTransition
}

func (m *mockToolTransitionStore) Increment(_ context.Context, _, _, _ string) error { return nil }
func (m *mockToolTransitionStore) IncrementWithOutcome(_ context.Context, _, _, _, _ string) error {
	return nil
}
func (m *mockToolTransitionStore) GetTransitions(_ context.Context, params store.GetTransitionsParams) ([]store.ToolTransition, error) {
	var result []store.ToolTransition
	for _, t := range m.transitions {
		if t.FromTool == params.FromTool {
			result = append(result, t)
		}
	}
	return result, nil
}
func (m *mockToolTransitionStore) GetDeadEnds(_ context.Context, _ string) ([]store.ToolTransition, error) {
	return m.deadEnds, nil
}
func (m *mockToolTransitionStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

// mockWorkflowTemplateStore for ranking tests.
type mockWorkflowTemplateStore struct {
	templates []store.WorkflowTemplate
}

func (m *mockWorkflowTemplateStore) Seed(_ context.Context, _ []store.WorkflowTemplate) error {
	return nil
}
func (m *mockWorkflowTemplateStore) GetNextStep(_ context.Context, intent string, stepOrder int) ([]store.WorkflowTemplate, error) {
	var result []store.WorkflowTemplate
	for _, t := range m.templates {
		if t.Intent == intent && t.StepOrder == stepOrder {
			result = append(result, t)
		}
	}
	return result, nil
}
func (m *mockWorkflowTemplateStore) GetByName(_ context.Context, name string) ([]store.WorkflowTemplate, error) {
	var result []store.WorkflowTemplate
	for _, t := range m.templates {
		if t.Name == name {
			result = append(result, t)
		}
	}
	return result, nil
}
func (m *mockWorkflowTemplateStore) List(_ context.Context, intent string) ([]store.WorkflowTemplate, error) {
	var result []store.WorkflowTemplate
	for _, t := range m.templates {
		if t.Intent == intent {
			result = append(result, t)
		}
	}
	return result, nil
}

func TestComputeScore(t *testing.T) {
	// Recent, fully resolved transition
	t1 := store.ToolTransition{
		TotalCount:    10,
		ResolvedCount: 8,
		AbandonedCount: 1,
		LastSeenAt:    time.Now(),
	}
	score1 := computeScore(t1)
	if score1 < 0.5 {
		t.Errorf("expected high score for mostly-resolved transition, got %f", score1)
	}

	// Mostly abandoned transition
	t2 := store.ToolTransition{
		TotalCount:     10,
		ResolvedCount:  1,
		AbandonedCount: 8,
		LastSeenAt:     time.Now(),
	}
	score2 := computeScore(t2)
	if score2 > score1 {
		t.Errorf("abandoned transition should score lower: %f > %f", score2, score1)
	}

	// Old transition should decay
	t3 := store.ToolTransition{
		TotalCount:    10,
		ResolvedCount: 8,
		AbandonedCount: 1,
		LastSeenAt:    time.Now().Add(-60 * 24 * time.Hour), // 60 days ago
	}
	score3 := computeScore(t3)
	if score3 >= score1 {
		t.Errorf("old transition should score lower: %f >= %f", score3, score1)
	}

	// Zero total should return 0
	t4 := store.ToolTransition{TotalCount: 0}
	if computeScore(t4) != 0 {
		t.Errorf("expected 0 for zero total")
	}
}

func TestRankingService_LearnedTransitions(t *testing.T) {
	ts := &mockToolTransitionStore{
		transitions: []store.ToolTransition{
			{FromTool: "diagnose", ToTool: "error_groups", TotalCount: 10, ResolvedCount: 8, LastSeenAt: time.Now()},
			{FromTool: "diagnose", ToTool: "log_search", TotalCount: 5, ResolvedCount: 2, LastSeenAt: time.Now()},
		},
	}
	ws := &mockWorkflowTemplateStore{}

	rs := NewRankingService(ts, ws)

	result := rs.RankSuggestions(context.Background(), RankingRequest{
		CurrentTool: "diagnose",
		Intent:      "investigation",
		StepIndex:   2,
		FallbackSuggestions: []ToolSuggestion{
			{Tool: "error_groups", Why: "fallback reason", Args: map[string]any{"limit": 10}},
		},
	})

	if len(result) == 0 {
		t.Fatal("expected ranked suggestions")
	}
	if result[0].Source != "learned" {
		t.Errorf("expected source=learned, got %q", result[0].Source)
	}
	// First result should be error_groups (higher resolved count)
	if result[0].Tool != "error_groups" {
		t.Errorf("expected error_groups first, got %q", result[0].Tool)
	}
	// Should have merged args from fallback
	if result[0].Args == nil || result[0].Args["limit"] != 10 {
		t.Errorf("expected merged args from fallback, got %v", result[0].Args)
	}
}

func TestRankingService_TemplateFallback(t *testing.T) {
	ts := &mockToolTransitionStore{} // no transitions
	ws := &mockWorkflowTemplateStore{
		templates: []store.WorkflowTemplate{
			{Intent: "investigation", Name: "error_spike", StepOrder: 0, ToolName: "diagnose"},
		},
	}

	rs := NewRankingService(ts, ws)

	result := rs.RankSuggestions(context.Background(), RankingRequest{
		CurrentTool: "",
		Intent:      "investigation",
		StepIndex:   0,
		FallbackSuggestions: []ToolSuggestion{
			{Tool: "system_overview", Why: "static fallback"},
		},
	})

	if len(result) == 0 {
		t.Fatal("expected template suggestions")
	}
	if result[0].Source != "template" {
		t.Errorf("expected source=template, got %q", result[0].Source)
	}
}

func TestRankingService_StaticFallback(t *testing.T) {
	ts := &mockToolTransitionStore{} // no transitions
	ws := &mockWorkflowTemplateStore{} // no templates

	rs := NewRankingService(ts, ws)

	fallback := []ToolSuggestion{
		{Tool: "system_overview", Why: "static", Source: "static"},
	}

	result := rs.RankSuggestions(context.Background(), RankingRequest{
		CurrentTool:         "some_tool",
		Intent:              "query",
		StepIndex:           5,
		FallbackSuggestions: fallback,
	})

	if len(result) != 1 {
		t.Fatalf("expected 1 fallback, got %d", len(result))
	}
	if result[0].Tool != "system_overview" {
		t.Errorf("expected static fallback tool, got %q", result[0].Tool)
	}
}

func TestRankingService_DeadEndDetection(t *testing.T) {
	ts := &mockToolTransitionStore{
		deadEnds: []store.ToolTransition{
			{FromTool: "log_search", ToTool: "trace_lookup", TotalCount: 10, AbandonedCount: 8, ResolvedCount: 1},
		},
	}

	rs := NewRankingService(ts, nil)

	warnings := rs.GetDeadEnds(context.Background(), "investigation")
	if len(warnings) != 1 {
		t.Fatalf("expected 1 dead-end warning, got %d", len(warnings))
	}
	if warnings[0].FromTool != "log_search" {
		t.Errorf("expected from_tool=log_search, got %q", warnings[0].FromTool)
	}
}
