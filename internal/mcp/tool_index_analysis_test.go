package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestDbIndexAnalysisHandler_UnusedIndexes(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&capturingMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{"schemaname", "table_name", "index_name", "size_bytes", "idx_scan"},
			Rows: [][]any{
				{"public", "orders", "idx_orders_legacy", int64(524288000), int64(0)},
			},
			RowCount: 1,
		},
	})

	handler := dbIndexAnalysisHandler(registry)
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

	unused, ok := resp["unused_indexes"].([]any)
	if !ok {
		t.Fatal("expected unused_indexes field")
	}
	if len(unused) != 1 {
		t.Errorf("expected 1 unused index, got %d", len(unused))
	}
	if len(unused) > 0 {
		entry := unused[0].(map[string]any)
		if entry["index_name"] != "idx_orders_legacy" {
			t.Errorf("index_name = %v, want idx_orders_legacy", entry["index_name"])
		}
		if _, ok := entry["suggestion"]; !ok {
			t.Error("expected suggestion field")
		}
	}

	summary, ok := resp["summary"].(map[string]any)
	if !ok {
		t.Fatal("expected summary field")
	}
	if summary["unused"].(float64) != 1 {
		t.Errorf("summary.unused = %v, want 1", summary["unused"])
	}
}

func TestDbIndexAnalysisHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()

	handler := dbIndexAnalysisHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector is active")
	}
}

func TestDbIndexAnalysisHandler_NoSuggestions(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&capturingMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{"schemaname", "table_name", "index_name", "size_bytes", "idx_scan"},
			Rows: [][]any{
				{"public", "orders", "idx_orders_legacy", int64(1024), int64(0)},
			},
			RowCount: 1,
		},
	})

	handler := dbIndexAnalysisHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"include_suggestions": false,
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

	unused, ok := resp["unused_indexes"].([]any)
	if !ok || len(unused) == 0 {
		t.Fatal("expected unused_indexes")
	}
	entry := unused[0].(map[string]any)
	if _, ok := entry["suggestion"]; ok {
		t.Error("expected no suggestion when include_suggestions=false")
	}
}

func TestDbIndexAnalysisHandler_QueryError(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&errorMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            fmt.Errorf("connection refused"),
	})

	handler := dbIndexAnalysisHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error from query failure")
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{524288000, "500.0 MB"},
	}
	for _, tt := range tests {
		got := humanSize(tt.input)
		if got != tt.expected {
			t.Errorf("humanSize(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSanitizeIdentifier(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"orders", "orders"},
		{"public.orders", "publicorders"},
		{"orders; DROP TABLE--", "ordersDROPTABLE"},
		{"my_table_123", "my_table_123"},
	}
	for _, tt := range tests {
		got := sanitizeIdentifier(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeIdentifier(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
