package tools

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestCodeIntelHandler_UnknownAction(t *testing.T) {
	deps := CodeIntelDeps{
		CodeEntityStore:          mocks.NewCodeEntityStore(),
		ErrorGroupStore:          mocks.NewErrorGroupStore(),
		TestCorrelationStore:     mocks.NewTestCorrelationStore(),
		DeployStore:              mocks.NewDeployStore(),
		AgentNoteStore:           mocks.NewAgentNoteStore(),
		InvestigationSessionStore: mocks.NewInvestigationSessionStore(),
	}
	handler := CodeIntelHandler(deps)

	req := MakeCallToolRequest("code_intel", map[string]any{"action": "nonexistent"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true for unknown action")
	}
}

func TestHandleCodeRisk_Empty(t *testing.T) {
	deps := CodeIntelDeps{
		CodeEntityStore:          mocks.NewCodeEntityStore(),
		ErrorGroupStore:          mocks.NewErrorGroupStore(),
		TestCorrelationStore:     mocks.NewTestCorrelationStore(),
		DeployStore:              mocks.NewDeployStore(),
		AgentNoteStore:           mocks.NewAgentNoteStore(),
		InvestigationSessionStore: mocks.NewInvestigationSessionStore(),
	}

	args := map[string]any{
		"action": "risk",
		"files":  []any{"app/controllers/foo.rb", "app/models/bar.rb"},
	}
	result, err := HandleCodeRisk(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Errorf("expected IsError to be false, got true: %s", extractText(t, result))
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestHandleFragile_Empty(t *testing.T) {
	deps := CodeIntelDeps{
		CodeEntityStore:          mocks.NewCodeEntityStore(),
		ErrorGroupStore:          mocks.NewErrorGroupStore(),
		TestCorrelationStore:     mocks.NewTestCorrelationStore(),
		DeployStore:              mocks.NewDeployStore(),
		AgentNoteStore:           mocks.NewAgentNoteStore(),
		InvestigationSessionStore: mocks.NewInvestigationSessionStore(),
	}

	args := map[string]any{"action": "fragile"}
	result, err := HandleFragile(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Errorf("expected IsError to be false, got true: %s", extractText(t, result))
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestHandleTestGaps_Empty(t *testing.T) {
	deps := CodeIntelDeps{
		CodeEntityStore:          mocks.NewCodeEntityStore(),
		ErrorGroupStore:          mocks.NewErrorGroupStore(),
		TestCorrelationStore:     mocks.NewTestCorrelationStore(),
		DeployStore:              mocks.NewDeployStore(),
		AgentNoteStore:           mocks.NewAgentNoteStore(),
		InvestigationSessionStore: mocks.NewInvestigationSessionStore(),
	}

	args := map[string]any{"action": "test_gaps"}
	result, err := HandleTestGaps(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Errorf("expected IsError to be false, got true: %s", extractText(t, result))
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestHandleTestPriority_Empty(t *testing.T) {
	deps := CodeIntelDeps{
		CodeEntityStore:          mocks.NewCodeEntityStore(),
		ErrorGroupStore:          mocks.NewErrorGroupStore(),
		TestCorrelationStore:     mocks.NewTestCorrelationStore(),
		DeployStore:              mocks.NewDeployStore(),
		AgentNoteStore:           mocks.NewAgentNoteStore(),
		InvestigationSessionStore: mocks.NewInvestigationSessionStore(),
	}

	args := map[string]any{
		"action":      "test_priority",
		"fingerprint": "abc123",
	}
	result, err := HandleTestPriority(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Errorf("expected IsError to be false, got true: %s", extractText(t, result))
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestHandleCodeContext_MissingEntityName(t *testing.T) {
	deps := CodeIntelDeps{
		CodeEntityStore: mocks.NewCodeEntityStore(),
	}

	// No entity_name and no task → should return error.
	args := map[string]any{}
	result, err := HandleCodeContext(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when entity_name is missing")
	}
}

func TestHandleCodeContext_Empty(t *testing.T) {
	deps := CodeIntelDeps{
		CodeEntityStore:      mocks.NewCodeEntityStore(),
		ErrorGroupStore:      mocks.NewErrorGroupStore(),
		TestCorrelationStore: mocks.NewTestCorrelationStore(),
		DeployStore:          mocks.NewDeployStore(),
		AgentNoteStore:       mocks.NewAgentNoteStore(),
	}

	// entity_name provided but not found in empty store.
	args := map[string]any{"entity_name": "app/models/user.rb"}
	result, err := HandleCodeContext(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Errorf("expected IsError to be false, got true: %s", extractText(t, result))
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestHandleTaskContext_MissingTask(t *testing.T) {
	deps := CodeIntelDeps{
		CodeEntityStore: mocks.NewCodeEntityStore(),
	}

	// When task is provided via HandleCodeContext, it delegates to HandleTaskContext.
	// But if task is empty and entity_name is also empty, it errors on entity_name.
	// To test HandleTaskContext directly, call it with an empty task string.
	args := map[string]any{}
	result, err := HandleTaskContext(context.Background(), deps, args, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Even with empty task, HandleTaskContext returns a result (classifies as "editing").
	if result.IsError {
		t.Errorf("expected IsError to be false, got true: %s", extractText(t, result))
	}
}

func TestClassifyTaskType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"fix bug X", "debugging"},
		{"deploy to production", "deploying"},
		{"test the login flow", "testing"},
		{"review PR #42", "reviewing"},
		{"add feature for users", "editing"},
		{"unknown task", "editing"},
		{"investigating a crash", "debugging"},
		{"rollback the release", "deploying"},
		{"write specs for auth", "testing"},
		{"code review session", "reviewing"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := classifyTaskType(tt.input)
			if got != tt.want {
				t.Errorf("classifyTaskType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
