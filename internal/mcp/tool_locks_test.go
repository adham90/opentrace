package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestDbLocksHandler_NoLocks(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&previewMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"blocking_pid", "blocking_app", "blocking_state", "blocking_query", "blocking_duration_seconds", "blocked_pid", "blocked_app", "blocked_state", "blocked_query", "blocked_duration_seconds", "lock_type", "relation"},
			Rows:     [][]any{},
			RowCount: 0,
		},
	})

	handler := dbLocksHandler(registry)
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

	if resp["total_chains"] != float64(0) {
		t.Errorf("total_chains = %v, want 0", resp["total_chains"])
	}
	if _, ok := resp["message"]; !ok {
		t.Error("expected message field when no locks")
	}
}

func TestDbLocksHandler_WithBlockingChains(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&previewMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{"blocking_pid", "blocking_app", "blocking_state", "blocking_query", "blocking_duration_seconds", "blocked_pid", "blocked_app", "blocked_state", "blocked_query", "blocked_duration_seconds", "lock_type", "relation"},
			Rows: [][]any{
				{int64(1234), "web-api", "idle in transaction", "UPDATE accounts SET balance = 100", int64(120), int64(5678), "worker", "active", "UPDATE accounts SET balance = 200", int64(45), "relation", "accounts"},
			},
			RowCount: 1,
		},
	})

	handler := dbLocksHandler(registry)
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

	if resp["total_chains"] != float64(1) {
		t.Errorf("total_chains = %v, want 1", resp["total_chains"])
	}

	chains, ok := resp["blocking_chains"].([]any)
	if !ok || len(chains) != 1 {
		t.Fatalf("expected 1 blocking chain, got %v", resp["blocking_chains"])
	}

	chain := chains[0].(map[string]any)
	if chain["blocking_pid"] != float64(1234) {
		t.Errorf("blocking_pid = %v, want 1234", chain["blocking_pid"])
	}
	if chain["blocked_pid"] != float64(5678) {
		t.Errorf("blocked_pid = %v, want 5678", chain["blocked_pid"])
	}

	// Should warn about idle-in-transaction blocker.
	warnings, ok := resp["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Error("expected warnings for idle in transaction blocker")
	}
}

func TestDbLocksHandler_AllLocks(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&previewMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{"locktype", "relation", "mode", "granted", "pid", "app_name", "state", "query_preview"},
			Rows: [][]any{
				{"relation", "users", "AccessShareLock", true, int64(100), "web-api", "active", "SELECT * FROM users"},
				{"relation", "orders", "RowExclusiveLock", true, int64(101), "worker", "active", "UPDATE orders SET status = 'done'"},
			},
			RowCount: 2,
		},
	})

	handler := dbLocksHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"blocking_only": false,
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

	if resp["total_locks"] != float64(2) {
		t.Errorf("total_locks = %v, want 2", resp["total_locks"])
	}
}

func TestDbLocksHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := dbLocksHandler(registry)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector is active")
	}
}

func TestDbLocksHandler_QueryError(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&errorMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            fmt.Errorf("permission denied"),
	})

	handler := dbLocksHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for query failure")
	}
}
