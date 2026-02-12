package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestSchemaOverviewHandler_AllTables(t *testing.T) {
	registry := connector.NewRegistry()

	callCount := 0
	registry.Register(&multiQueryMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		handler: func(query string) (*connector.QueryResult, error) {
			callCount++
			if callCount == 1 {
				return &connector.QueryResult{
					Columns:  []string{"table_name", "column_count", "size", "comment", "estimated_rows"},
					Rows:     [][]any{{"users", int64(5), "2 MB", nil, int64(10000)}, {"orders", int64(8), "10 MB", "order data", int64(50000)}},
					RowCount: 2,
				}, nil
			}
			// Dependencies query.
			return &connector.QueryResult{
				Columns:  []string{"from_table", "to_table", "from_column", "to_column"},
				Rows:     [][]any{{"orders", "users", "user_id", "id"}},
				RowCount: 1,
			}, nil
		},
	})

	handler := schemaOverviewHandler(registry)
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

	if resp["table_count"] != float64(2) {
		t.Errorf("table_count = %v, want 2", resp["table_count"])
	}
	if resp["schema"] != "public" {
		t.Errorf("schema = %v, want public", resp["schema"])
	}

	// Check dependencies are included.
	deps, ok := resp["dependencies"].([]any)
	if !ok {
		t.Fatal("expected dependencies array")
	}
	if len(deps) != 1 {
		t.Errorf("expected 1 dependency, got %d", len(deps))
	}
}

func TestSchemaOverviewHandler_CustomSchema(t *testing.T) {
	registry := connector.NewRegistry()

	callCount := 0
	registry.Register(&multiQueryMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		handler: func(query string) (*connector.QueryResult, error) {
			callCount++
			if callCount == 1 {
				return &connector.QueryResult{
					Columns:  []string{"table_name", "column_count", "size", "comment", "estimated_rows"},
					Rows:     [][]any{{"events", int64(3), "500 kB", nil, int64(100)}},
					RowCount: 1,
				}, nil
			}
			return &connector.QueryResult{Columns: []string{"x"}, Rows: [][]any{}, RowCount: 0}, nil
		},
	})

	handler := schemaOverviewHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"schema": "analytics",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["schema"] != "analytics" {
		t.Errorf("schema = %v, want analytics", resp["schema"])
	}
}

func TestSchemaOverviewHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := schemaOverviewHandler(registry)

	result, err := handler(context.Background(), makeRequest(nil))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector active")
	}
}

func TestSchemaOverviewHandler_Empty(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"table_name", "column_count", "size", "comment", "estimated_rows"},
			Rows:     [][]any{},
			RowCount: 0,
		},
	})

	handler := schemaOverviewHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success for empty schema")
	}

	text := resultText(t, result)
	if !contains(text, "No tables found") {
		t.Errorf("expected 'No tables found' message, got: %s", text)
	}
}
