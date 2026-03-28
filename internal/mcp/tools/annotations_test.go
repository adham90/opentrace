package tools

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestAnnotationsHandler_UnknownAction(t *testing.T) {
	deps := AnnotationsDeps{
		AnalyticsStore:  mocks.NewAnalyticsStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
		CodeEntityStore: mocks.NewCodeEntityStore(),
		LogStore:        mocks.NewLogStore(),
	}
	handler := AnnotationsHandler(deps)

	req := MakeCallToolRequest("annotations", map[string]any{"action": "nonexistent"})
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

func TestHandleAnnotationsFile_MissingPath(t *testing.T) {
	deps := AnnotationsDeps{
		AnalyticsStore:  mocks.NewAnalyticsStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
		CodeEntityStore: mocks.NewCodeEntityStore(),
		LogStore:        mocks.NewLogStore(),
	}

	args := map[string]any{"action": "file"}
	result, err := HandleAnnotationsFile(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when path is missing")
	}
}

func TestHandleAnnotationsFunction_MissingPath(t *testing.T) {
	deps := AnnotationsDeps{
		AnalyticsStore:  mocks.NewAnalyticsStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
		CodeEntityStore: mocks.NewCodeEntityStore(),
		LogStore:        mocks.NewLogStore(),
	}

	// Neither controller nor endpoint provided
	args := map[string]any{"action": "function"}
	result, err := HandleAnnotationsFunction(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when controller and endpoint are missing")
	}
}

func TestHandleAnnotationsHotspots_Empty(t *testing.T) {
	deps := AnnotationsDeps{
		AnalyticsStore:  mocks.NewAnalyticsStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
		CodeEntityStore: mocks.NewCodeEntityStore(),
		LogStore:        mocks.NewLogStore(),
	}

	args := map[string]any{"action": "hotspots"}
	result, err := HandleAnnotationsHotspots(context.Background(), deps, args)
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
