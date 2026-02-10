package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// mockQueryExecutor implements both connector.DataSource and connector.QueryExecutor.
type mockQueryExecutor struct {
	mockDataSource
	result *connector.QueryResult
	err    error
}

func (m *mockQueryExecutor) ExecuteReadQuery(ctx context.Context, query string) (*connector.QueryResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func TestQueryStatsHandler_Success(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{"queryid", "query_preview", "calls", "total_exec_time", "mean_exec_time", "rows", "shared_blks_hit", "shared_blks_read"},
			Rows: [][]any{
				{int64(123), "SELECT * FROM users", int64(500), 1234.5, 2.469, int64(5000), int64(9000), int64(100)},
				{int64(456), "SELECT * FROM orders", int64(200), 800.0, 4.0, int64(2000), int64(4000), int64(50)},
			},
			RowCount: 2,
		},
	})

	handler := queryStatsHandler(registry, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"order_by": "calls",
		"limit":    float64(10),
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
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	if resp["order_by"] != "calls" {
		t.Errorf("order_by = %v, want calls", resp["order_by"])
	}
	if resp["total_found"] != float64(2) {
		t.Errorf("total_found = %v, want 2", resp["total_found"])
	}

	queries, ok := resp["queries"].([]any)
	if !ok {
		t.Fatal("expected queries array")
	}
	if len(queries) != 2 {
		t.Errorf("len(queries) = %d, want 2", len(queries))
	}
}

func TestQueryStatsHandler_Empty(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"queryid"},
			Rows:     [][]any{},
			RowCount: 0,
		},
	})

	handler := queryStatsHandler(registry, nil)
	result, err := handler(context.Background(), makeRequest(nil))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "No query statistics found") {
		t.Errorf("expected empty message, got: %s", text)
	}
}

func TestQueryStatsHandler_ExtensionNotInstalled(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            errors.New("relation \"pg_stat_statements\" does not exist"),
	})

	handler := queryStatsHandler(registry, nil)
	result, err := handler(context.Background(), makeRequest(nil))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected graceful text response, not error")
	}

	text := resultText(t, result)
	if !contains(text, "pg_stat_statements extension is not enabled") {
		t.Errorf("expected extension help message, got: %s", text)
	}
}

func TestQueryStatsHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := queryStatsHandler(registry, nil)

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

func TestQueryStatsHandler_NotQueryExecutor(t *testing.T) {
	registry := connector.NewRegistry()
	// Register a plain mockDataSource without QueryExecutor
	registry.Register(&mockDataSource{connType: connector.ConnectorDatabase})

	handler := queryStatsHandler(registry, nil)
	result, err := handler(context.Background(), makeRequest(nil))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when connector doesn't implement QueryExecutor")
	}

	text := resultText(t, result)
	if !contains(text, "does not support direct queries") {
		t.Errorf("expected not-supported message, got: %s", text)
	}
}

func TestQueryStatsHandler_WithExistingMonitors(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{"queryid", "query_preview", "calls", "total_exec_time", "mean_exec_time", "rows", "shared_blks_hit", "shared_blks_read"},
			Rows: [][]any{
				{int64(123), "SELECT * FROM users", int64(500), 1234.5, 2.469, int64(5000), int64(9000), int64(100)},
			},
			RowCount: 1,
		},
	})

	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: uuid.New(), Title: "Slow query monitor", Status: store.WatcherActive, MonitorType: store.MonitorTypeRule,
				RuleConfig: &store.RuleConfig{Source: store.RuleSourceQuery, Query: "SELECT count(*) FROM pg_stat_activity"}},
			{ID: uuid.New(), Title: "Error log monitor", Status: store.WatcherActive, MonitorType: store.MonitorTypeAI},
		},
	}

	handler := queryStatsHandler(registry, ws)
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

	monitors, ok := resp["existing_monitors"].([]any)
	if !ok {
		t.Fatal("expected existing_monitors array in response")
	}
	if len(monitors) != 2 {
		t.Errorf("len(existing_monitors) = %d, want 2", len(monitors))
	}

	if _, ok := resp["existing_monitors_hint"]; !ok {
		t.Error("expected existing_monitors_hint in response")
	}
}
