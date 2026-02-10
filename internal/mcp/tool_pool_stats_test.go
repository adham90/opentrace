package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestConnectionPoolStatsHandler_Success(t *testing.T) {
	registry := connector.NewRegistry()
	// The mock will be called multiple times for different queries.
	// capturingMockQE returns the same result for every query, but that's OK
	// for basic structural testing.
	registry.Register(&capturingMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"value"},
			Rows:     [][]any{{"100"}},
			RowCount: 1,
		},
	})

	handler := connectionPoolStatsHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{}))
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

	connectors, ok := resp["connectors"].([]any)
	if !ok {
		t.Fatal("expected connectors field")
	}
	if len(connectors) != 1 {
		t.Errorf("expected 1 connector, got %d", len(connectors))
	}
}

func TestConnectionPoolStatsHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()

	handler := connectionPoolStatsHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector is active")
	}
}

func TestConnectionPoolStatsHandler_QueryError(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&errorMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            fmt.Errorf("connection refused"),
	})

	handler := connectionPoolStatsHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error from query failure")
	}
}
