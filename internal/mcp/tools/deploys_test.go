package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestDeploysHandler_UnknownAction(t *testing.T) {
	d := DeploysDeps{
		DeployStore: mocks.NewDeployStore(),
	}

	handler := DeploysHandler(d)
	req := MakeCallToolRequest("deploys", map[string]any{"action": "bogus"})
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

func TestHandleDeployHistory_Empty(t *testing.T) {
	d := DeploysDeps{
		DeployStore: mocks.NewDeployStore(),
	}

	args := map[string]any{}
	result, err := HandleDeployHistory(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	text := extractText(t, result)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
	if !strings.Contains(text, "No deploys recorded") {
		t.Errorf("expected 'No deploys recorded' message, got %q", text)
	}
}

func TestHandleDeployRecord_MissingCommit(t *testing.T) {
	d := DeploysDeps{
		DeployStore: mocks.NewDeployStore(),
	}

	// Provide service but omit commit.
	args := map[string]any{
		"service": "web",
	}
	result, err := HandleDeployRecord(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true for missing commit")
	}

	text := extractText(t, result)
	if !strings.Contains(text, "commit is required") {
		t.Errorf("expected 'commit is required' error, got %q", text)
	}
}

func TestHandleDeployImpact_MissingID(t *testing.T) {
	d := DeploysDeps{
		DeployStore: mocks.NewDeployStore(),
	}

	// No commit_hash provided.
	args := map[string]any{}
	result, err := HandleDeployImpact(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true for missing commit_hash")
	}

	text := extractText(t, result)
	if !strings.Contains(text, "commit_hash is required") {
		t.Errorf("expected 'commit_hash is required' error, got %q", text)
	}
}
