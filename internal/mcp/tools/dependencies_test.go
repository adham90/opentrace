package tools

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestDependenciesHandler_UnknownAction(t *testing.T) {
	deps := DependenciesDeps{
		AnalyticsStore:  mocks.NewAnalyticsStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
		CodeEntityStore: mocks.NewCodeEntityStore(),
		DeployStore:     mocks.NewDeployStore(),
		LogStore:        mocks.NewLogStore(),
	}
	handler := DependenciesHandler(deps)

	req := MakeCallToolRequest("dependencies", map[string]any{"action": "nonexistent"})
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

func TestHandleDepsService_MissingService(t *testing.T) {
	deps := DependenciesDeps{
		AnalyticsStore:  mocks.NewAnalyticsStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
		CodeEntityStore: mocks.NewCodeEntityStore(),
		DeployStore:     mocks.NewDeployStore(),
		LogStore:        mocks.NewLogStore(),
	}

	args := map[string]any{"action": "service"}
	result, err := HandleDepsService(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when service is missing")
	}
}

func TestHandleDepsBlastRadius_MissingService(t *testing.T) {
	deps := DependenciesDeps{
		AnalyticsStore:  mocks.NewAnalyticsStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
		CodeEntityStore: mocks.NewCodeEntityStore(),
		DeployStore:     mocks.NewDeployStore(),
		LogStore:        mocks.NewLogStore(),
	}

	args := map[string]any{"action": "blast_radius"}
	result, err := HandleDepsBlastRadius(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when service is missing")
	}
}
