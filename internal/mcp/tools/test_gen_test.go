package tools

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestTestGenHandler_UnknownAction(t *testing.T) {
	deps := TestGenDeps{
		ErrorGroupStore:      mocks.NewErrorGroupStore(),
		ErrorImpactStore:     mocks.NewErrorImpactStore(),
		CodeEntityStore:      mocks.NewCodeEntityStore(),
		TestCorrelationStore: mocks.NewTestCorrelationStore(),
		LogStore:             mocks.NewLogStore(),
	}
	handler := TestGenHandler(deps)

	req := MakeCallToolRequest("test_gen", map[string]any{"action": "nonexistent"})
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

func TestHandleTestGenContext_MissingFingerprint(t *testing.T) {
	deps := TestGenDeps{
		ErrorGroupStore:      mocks.NewErrorGroupStore(),
		ErrorImpactStore:     mocks.NewErrorImpactStore(),
		CodeEntityStore:      mocks.NewCodeEntityStore(),
		TestCorrelationStore: mocks.NewTestCorrelationStore(),
		LogStore:             mocks.NewLogStore(),
	}

	args := map[string]any{"action": "context"}
	result, err := HandleTestGenContext(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when fingerprint is missing")
	}
}

func TestHandleTestGenCoverage_Empty(t *testing.T) {
	deps := TestGenDeps{
		ErrorGroupStore:      mocks.NewErrorGroupStore(),
		ErrorImpactStore:     mocks.NewErrorImpactStore(),
		CodeEntityStore:      mocks.NewCodeEntityStore(),
		TestCorrelationStore: mocks.NewTestCorrelationStore(),
		LogStore:             mocks.NewLogStore(),
	}

	args := map[string]any{"action": "coverage"}
	result, err := HandleTestGenCoverage(context.Background(), deps, args)
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
