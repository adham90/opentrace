package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestBloatEstimateHandler_Success(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{"table_name", "total_size", "total_bytes", "n_live_tup", "n_dead_tup", "dead_pct", "estimated_bloat", "estimated_bloat_bytes", "last_vacuum", "last_autovacuum"},
			Rows: [][]any{
				{"public.users", "500 MB", int64(524288000), int64(100000), int64(35000), 25.93, "129 MB", int64(135790694), nil, "2024-01-15T10:00:00Z"},
				{"public.orders", "200 MB", int64(209715200), int64(50000), int64(5000), 9.09, "19 MB", int64(19065382), "2024-01-14T10:00:00Z", nil},
			},
			RowCount: 2,
		},
	})

	handler := bloatEstimateHandler(registry)
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

	if resp["total_tables"] != float64(2) {
		t.Errorf("total_tables = %v, want 2", resp["total_tables"])
	}

	// Check recommendations exist (public.users has >10% dead tuples)
	recs, ok := resp["recommendations"].([]any)
	if !ok {
		t.Fatal("expected recommendations array")
	}
	if len(recs) == 0 {
		t.Error("expected at least one recommendation for table with high dead tuple ratio")
	}
}

func TestBloatEstimateHandler_Empty(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"table_name"},
			Rows:     [][]any{},
			RowCount: 0,
		},
	})

	handler := bloatEstimateHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "No tables with significant bloat") {
		t.Errorf("expected empty message, got: %s", text)
	}
}

func TestBloatEstimateHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := bloatEstimateHandler(registry)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector")
	}
}

func TestBloatEstimateHandler_QueryError(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            errors.New("timeout"),
	})

	handler := bloatEstimateHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}
