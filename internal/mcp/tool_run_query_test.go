package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

// mockQueryExecutor is defined in tool_query_stats_test.go — reused here.

func TestRunQueryHandler_Success(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"id", "name"},
			Rows:     [][]any{{1, "alice"}, {2, "bob"}},
			RowCount: 2,
		},
	})

	handler := runQueryHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"query": "SELECT id, name FROM users",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "alice") {
		t.Errorf("expected 'alice' in output, got: %s", text)
	}
	if !contains(text, "2 row(s) returned") {
		t.Errorf("expected row count in output, got: %s", text)
	}
}

func TestRunQueryHandler_MissingQuery(t *testing.T) {
	registry := connector.NewRegistry()
	handler := runQueryHandler(registry)

	result, err := handler(context.Background(), makeRequest(map[string]any{}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing query")
	}
}

func TestRunQueryHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := runQueryHandler(registry)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"query": "SELECT 1",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector active")
	}
	text := resultText(t, result)
	if !contains(text, "No database connector") {
		t.Errorf("expected connector error message, got: %s", text)
	}
}

func TestRunQueryHandler_QueryError(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            errors.New("permission denied"),
	})

	handler := runQueryHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"query": "SELECT * FROM secret_table",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for query failure")
	}
	text := resultText(t, result)
	if !contains(text, "permission denied") {
		t.Errorf("expected query error message, got: %s", text)
	}
}
