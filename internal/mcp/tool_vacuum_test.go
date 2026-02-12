package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestVacuumReportHandler_Success(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{"schemaname", "relname", "n_live_tup", "n_dead_tup", "dead_tuple_pct",
				"last_vacuum", "last_autovacuum", "last_analyze", "last_autoanalyze",
				"vacuum_count", "autovacuum_count", "total_size"},
			Rows: [][]any{
				{"public", "users", int64(10000), int64(500), 4.76,
					nil, "2024-01-15 10:00:00", nil, "2024-01-15 10:00:00",
					int64(0), int64(5), "2 MB"},
				{"public", "orders", int64(50000), int64(15000), 23.08,
					nil, nil, nil, nil,
					int64(0), int64(0), "10 MB"},
			},
			RowCount: 2,
		},
	})

	handler := vacuumReportHandler(registry)
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

	recs, ok := resp["recommendations"].([]any)
	if !ok {
		t.Fatal("expected recommendations array")
	}
	// orders table has 23% dead tuples — should have a recommendation.
	foundOrders := false
	for _, r := range recs {
		if contains(r.(string), "orders") {
			foundOrders = true
		}
	}
	if !foundOrders {
		t.Error("expected recommendation for orders table (23% dead tuples)")
	}
}

func TestVacuumReportHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := vacuumReportHandler(registry)

	result, err := handler(context.Background(), makeRequest(nil))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector active")
	}
}

func TestVacuumReportHandler_QueryError(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            errors.New("query failed"),
	})

	handler := vacuumReportHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when query fails")
	}
}
