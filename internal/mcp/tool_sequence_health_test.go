package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestSequenceHealthHandler_Success(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{"sequence_name", "data_type", "last_value", "start_value", "min_value", "max_value", "increment_by", "cycle", "usage_pct"},
			Rows: [][]any{
				{"public.users_id_seq", "bigint", int64(50000), int64(1), int64(1), int64(9223372036854775807), int64(1), false, 0.0},
				{"public.orders_id_seq", "integer", int64(1500000000), int64(1), int64(1), int64(2147483647), int64(1), false, 69.85},
			},
			RowCount: 2,
		},
	})

	handler := sequenceHealthHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
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

	if resp["total"] != float64(2) {
		t.Errorf("total = %v, want 2", resp["total"])
	}

	// integer sequence at ~70% should trigger migration warning
	warnings, ok := resp["warnings"].([]any)
	if !ok {
		t.Fatal("expected warnings array")
	}
	found := false
	for _, w := range warnings {
		if contains(w.(string), "integer type") {
			found = true
		}
	}
	if !found {
		t.Error("expected warning about integer type sequence")
	}
}

func TestSequenceHealthHandler_Empty(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"sequence_name"},
			Rows:     [][]any{},
			RowCount: 0,
		},
	})

	handler := sequenceHealthHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "No sequences found") {
		t.Errorf("expected empty message, got: %s", text)
	}
}

func TestSequenceHealthHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := sequenceHealthHandler(registry)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector")
	}
}

func TestSequenceHealthHandler_QueryError(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            errors.New("permission denied"),
	})

	handler := sequenceHealthHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}
