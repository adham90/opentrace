package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

// multiQueryExecutor returns different results based on the query.
type multiQueryExecutor struct {
	mockDataSource
	queries map[string]*connector.QueryResult
	err     error
}

func (m *multiQueryExecutor) ExecuteReadQuery(ctx context.Context, query string) (*connector.QueryResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Match by prefix to handle queries with formatting differences.
	for prefix, result := range m.queries {
		if contains(query, prefix) {
			return result, nil
		}
	}
	// Return empty result for unmatched queries.
	return &connector.QueryResult{RowCount: 0}, nil
}

func TestDBActivityHandler_Success(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&multiQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		queries: map[string]*connector.QueryResult{
			"pg_stat_activity\nWHERE pid": {
				Columns:  []string{"state", "app_name", "count"},
				Rows:     [][]any{{"active", "myapp", int64(5)}, {"idle", "myapp", int64(10)}},
				RowCount: 2,
			},
			"10 seconds": {
				Columns:  []string{"pid", "app_name", "state", "duration_seconds", "query_preview"},
				Rows:     [][]any{{int64(1234), "myapp", "active", int64(30), "SELECT * FROM big_table"}},
				RowCount: 1,
			},
			"idle in transaction": {
				Columns:  []string{"pid", "app_name", "idle_seconds", "last_query"},
				Rows:     [][]any{{int64(5678), "worker", int64(120), "UPDATE accounts SET ..."}},
				RowCount: 1,
			},
			"max_connections": {
				Columns:  []string{"max_connections"},
				Rows:     [][]any{{"100"}},
				RowCount: 1,
			},
			"count(*) FROM pg_stat_activity": {
				Columns:  []string{"count"},
				Rows:     [][]any{{int64(85)}},
				RowCount: 1,
			},
		},
	})

	handler := dbActivityHandler(registry)
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
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	if resp["max_connections"] != float64(100) {
		t.Errorf("max_connections = %v, want 100", resp["max_connections"])
	}

	warnings, ok := resp["warnings"].([]any)
	if !ok {
		t.Fatal("expected warnings array")
	}

	// Should have warnings for long-running queries and idle-in-transaction.
	if len(warnings) < 2 {
		t.Errorf("expected at least 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestDBActivityHandler_HighUtilization(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&multiQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		queries: map[string]*connector.QueryResult{
			"pg_stat_activity\nWHERE pid": {
				Columns: []string{"state", "app_name", "count"},
				Rows:    [][]any{{"active", "myapp", int64(90)}},
				RowCount: 1,
			},
			"max_connections": {
				Columns:  []string{"max_connections"},
				Rows:     [][]any{{"100"}},
				RowCount: 1,
			},
			"count(*) FROM pg_stat_activity": {
				Columns:  []string{"count"},
				Rows:     [][]any{{int64(90)}},
				RowCount: 1,
			},
		},
	})

	handler := dbActivityHandler(registry)
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
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	utilization, ok := resp["utilization_percent"].(float64)
	if !ok {
		t.Fatal("expected utilization_percent")
	}
	if utilization != 90 {
		t.Errorf("utilization_percent = %v, want 90", utilization)
	}

	warnings, ok := resp["warnings"].([]any)
	if !ok {
		t.Fatal("expected warnings array")
	}

	found := false
	for _, w := range warnings {
		if contains(w.(string), "High connection utilization") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected high utilization warning, got: %v", warnings)
	}
}

func TestDBActivityHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := dbActivityHandler(registry)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector")
	}

	text := resultText(t, result)
	if !contains(text, "No database connector") {
		t.Errorf("expected no-connector message, got: %s", text)
	}
}

func TestDBActivityHandler_NotQueryExecutor(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockDataSource{connType: connector.ConnectorDatabase})

	handler := dbActivityHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when connector doesn't implement QueryExecutor")
	}
}
