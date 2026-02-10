package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestDBTableStatsHandler_Success(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{
				"schemaname", "table_name", "n_live_tup", "n_dead_tup",
				"seq_scan", "idx_scan", "n_tup_ins", "n_tup_upd", "n_tup_del",
				"last_vacuum", "last_autovacuum", "last_analyze", "last_autoanalyze",
				"heap_blks_hit", "heap_blks_read",
			},
			Rows: [][]any{
				{"public", "users", int64(50000), int64(200), int64(10), int64(5000), int64(50000), int64(1000), int64(100), nil, "2024-01-01", nil, "2024-01-01", int64(90000), int64(100)},
				{"public", "orders", int64(100000), int64(500), int64(100), int64(200), int64(100000), int64(5000), int64(200), nil, nil, nil, nil, int64(50000), int64(50000)},
			},
			RowCount: 2,
		},
	})

	handler := dbTableStatsHandler(registry, nil)
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

	tables, ok := resp["tables"].([]any)
	if !ok {
		t.Fatal("expected tables array")
	}
	if len(tables) != 2 {
		t.Errorf("len(tables) = %d, want 2", len(tables))
	}
}

func TestDBTableStatsHandler_Warnings(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{
				"schemaname", "table_name", "n_live_tup", "n_dead_tup",
				"seq_scan", "idx_scan", "n_tup_ins", "n_tup_upd", "n_tup_del",
				"last_vacuum", "last_autovacuum", "last_analyze", "last_autoanalyze",
				"heap_blks_hit", "heap_blks_read",
			},
			Rows: [][]any{
				// High dead tuples (30%), low index usage, low cache hit, never vacuumed.
				{"public", "bad_table", int64(100000), int64(40000), int64(500), int64(10), int64(100000), int64(5000), int64(200), nil, nil, nil, nil, int64(400), int64(600)},
			},
			RowCount: 1,
		},
	})

	handler := dbTableStatsHandler(registry, nil)
	result, err := handler(context.Background(), makeRequest(nil))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	warnings, ok := resp["warnings"].([]any)
	if !ok {
		t.Fatal("expected warnings array")
	}

	// Expect warnings for: dead tuples, low index usage, low cache hit, never vacuumed.
	if len(warnings) < 3 {
		t.Errorf("expected at least 3 warnings, got %d: %v", len(warnings), warnings)
	}

	foundDead := false
	foundIndex := false
	foundCache := false
	for _, w := range warnings {
		ws := w.(string)
		if contains(ws, "dead tuples") {
			foundDead = true
		}
		if contains(ws, "low index usage") {
			foundIndex = true
		}
		if contains(ws, "low cache hit") {
			foundCache = true
		}
	}
	if !foundDead {
		t.Error("expected dead tuple warning")
	}
	if !foundIndex {
		t.Error("expected low index usage warning")
	}
	if !foundCache {
		t.Error("expected low cache hit ratio warning")
	}
}

func TestDBTableStatsHandler_SingleTable(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{
				"schemaname", "table_name", "n_live_tup", "n_dead_tup",
				"seq_scan", "idx_scan", "n_tup_ins", "n_tup_upd", "n_tup_del",
				"last_vacuum", "last_autovacuum", "last_analyze", "last_autoanalyze",
				"heap_blks_hit", "heap_blks_read",
			},
			Rows: [][]any{
				{"public", "users", int64(50000), int64(100), int64(5), int64(5000), int64(50000), int64(1000), int64(100), "2024-01-01", "2024-01-01", "2024-01-01", "2024-01-01", int64(9900), int64(100)},
			},
			RowCount: 1,
		},
	})

	handler := dbTableStatsHandler(registry, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"table_name": "users",
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

	if resp["total_tables"] != float64(1) {
		t.Errorf("total_tables = %v, want 1", resp["total_tables"])
	}
}

func TestDBTableStatsHandler_Empty(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"schemaname"},
			Rows:     [][]any{},
			RowCount: 0,
		},
	})

	handler := dbTableStatsHandler(registry, nil)
	result, err := handler(context.Background(), makeRequest(nil))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "No user tables found") {
		t.Errorf("expected empty message, got: %s", text)
	}
}

func TestDBTableStatsHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := dbTableStatsHandler(registry, nil)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector")
	}
}

func TestDBTableStatsHandler_NotQueryExecutor(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockDataSource{connType: connector.ConnectorDatabase})

	handler := dbTableStatsHandler(registry, nil)
	result, err := handler(context.Background(), makeRequest(nil))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when connector doesn't implement QueryExecutor")
	}
}
