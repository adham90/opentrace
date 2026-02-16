package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestRunbookHandler_MissingPlaybook(t *testing.T) {
	registry := connector.NewRegistry()
	handler := runbookHandler(registry, nil)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing playbook")
	}
	text := resultText(t, result)
	if !contains(text, "playbook is required") {
		t.Errorf("expected playbook-required message, got: %s", text)
	}
}

func TestRunbookHandler_UnknownPlaybook(t *testing.T) {
	registry := connector.NewRegistry()
	handler := runbookHandler(registry, nil)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"playbook": "nonexistent",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for unknown playbook")
	}
	text := resultText(t, result)
	if !contains(text, "unknown playbook") {
		t.Errorf("expected unknown playbook message, got: %s", text)
	}
}

func TestRunbookHandler_SlowDB_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := runbookHandler(registry, nil)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"playbook": "slow_database",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector")
	}
}

func TestRunbookHandler_SlowDB_Success(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&multiQueryMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		handler: func(query string) (*connector.QueryResult, error) {
			// Return empty results for all queries — the runbook gracefully handles this.
			return &connector.QueryResult{
				Columns:  []string{"col"},
				Rows:     [][]any{},
				RowCount: 0,
			}, nil
		},
	})

	handler := runbookHandler(registry, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"playbook": "slow_database",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["playbook"] != "slow_database" {
		t.Errorf("playbook = %v, want slow_database", resp["playbook"])
	}

	diag, ok := resp["diagnosis"].([]any)
	if !ok {
		t.Fatal("expected diagnosis array")
	}
	if len(diag) == 0 {
		t.Error("expected at least one diagnosis entry")
	}
}

func TestRunbookHandler_ErrorSpike_Success(t *testing.T) {
	// error_spike doesn't need a db connector, it uses the log store.
	handler := runbookHandler(connector.NewRegistry(), &mockLogStore{})

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"playbook": "error_spike",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["playbook"] != "error_spike" {
		t.Errorf("playbook = %v, want error_spike", resp["playbook"])
	}
}

func TestRunbookHandler_ConnectionExhaustion_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := runbookHandler(registry, nil)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"playbook": "connection_exhaustion",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector")
	}
}
