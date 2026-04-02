package tools

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestCodeIntelHandler_UnknownAction(t *testing.T) {
	deps := CodeIntelDeps{
		CodeEntityStore: mocks.NewCodeEntityStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),

		AgentNoteStore:  mocks.NewAgentNoteStore(),
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
		CodeEntityStore: mocks.NewCodeEntityStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),

		AgentNoteStore:  mocks.NewAgentNoteStore(),
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
		CodeEntityStore: mocks.NewCodeEntityStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),

		AgentNoteStore:  mocks.NewAgentNoteStore(),
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
