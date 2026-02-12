package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestLongTransactionsHandler_Success(t *testing.T) {
	registry := connector.NewRegistry()

	callCount := 0
	registry.Register(&multiQueryMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		handler: func(query string) (*connector.QueryResult, error) {
			callCount++
			if callCount == 1 {
				// Main transaction query.
				return &connector.QueryResult{
					Columns: []string{"pid", "usename", "application_name", "client_addr", "state",
						"xact_duration_sec", "state_duration_sec", "query_duration_sec",
						"wait_event_type", "wait_event", "query_preview", "xact_start", "state_change"},
					Rows: [][]any{
						{int64(12345), "app_user", "myapp", "10.0.0.1", "idle in transaction",
							int64(600), int64(590), int64(0),
							nil, nil, "SELECT * FROM users", "2024-01-15T10:00:00Z", "2024-01-15T10:00:10Z"},
					},
					RowCount: 1,
				}, nil
			}
			// Lock query.
			return &connector.QueryResult{
				Columns: []string{"pid", "locktype", "mode", "relation", "granted"},
				Rows: [][]any{
					{int64(12345), "relation", "RowExclusiveLock", "users", true},
				},
				RowCount: 1,
			}, nil
		},
	})

	handler := longTransactionsHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"min_duration_seconds": float64(60),
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

	if resp["total"] != float64(1) {
		t.Errorf("total = %v, want 1", resp["total"])
	}
	if resp["idle_in_transaction"] != float64(1) {
		t.Errorf("idle_in_transaction = %v, want 1", resp["idle_in_transaction"])
	}

	// Should have warning about idle-in-transaction > 300s
	warnings, ok := resp["warnings"].([]any)
	if !ok {
		t.Fatal("expected warnings array")
	}
	if len(warnings) == 0 {
		t.Error("expected warning for idle-in-transaction session")
	}
}

func TestLongTransactionsHandler_NoneFound(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"pid"},
			Rows:     [][]any{},
			RowCount: 0,
		},
	})

	handler := longTransactionsHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "No transactions running longer than") {
		t.Errorf("expected no-transactions message, got: %s", text)
	}
}

func TestLongTransactionsHandler_DefaultDuration(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"pid"},
			Rows:     [][]any{},
			RowCount: 0,
		},
	})

	handler := longTransactionsHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "30 seconds") {
		t.Errorf("expected default 30 seconds, got: %s", text)
	}
}

func TestLongTransactionsHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := longTransactionsHandler(registry)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector")
	}
}

func TestLongTransactionsHandler_QueryError(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            errors.New("connection refused"),
	})

	handler := longTransactionsHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}
