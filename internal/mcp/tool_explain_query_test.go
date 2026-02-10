package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestExplainQueryHandler_ValidSelect(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&previewMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"QUERY PLAN"},
			Rows:     [][]any{{"Seq Scan on users  (cost=0.00..1.05 rows=5 width=36) (actual time=0.01..0.02 rows=5 loops=1)"}},
			RowCount: 1,
		},
	})

	handler := explainQueryHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"query": "SELECT * FROM users",
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

	if resp["query"] != "SELECT * FROM users" {
		t.Errorf("query = %v, want SELECT * FROM users", resp["query"])
	}
	if _, ok := resp["plan"]; !ok {
		t.Error("expected plan field in response")
	}
	// Seq scan in plan should trigger a warning.
	if warnings, ok := resp["warnings"].([]any); ok {
		if len(warnings) == 0 {
			t.Error("expected warnings for seq scan")
		}
	} else {
		t.Error("expected warnings array")
	}
}

func TestExplainQueryHandler_NonSelectRejected(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&previewMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result:         &connector.QueryResult{},
	})

	handler := explainQueryHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"query": "DELETE FROM users WHERE id = 1",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for non-SELECT query")
	}
	text := resultText(t, result)
	if !contains(text, "only SELECT") {
		t.Errorf("error should mention SELECT, got: %s", text)
	}
}

func TestExplainQueryHandler_MissingQuery(t *testing.T) {
	registry := connector.NewRegistry()
	handler := explainQueryHandler(registry)

	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing query")
	}
}

func TestExplainQueryHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := explainQueryHandler(registry)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"query": "SELECT 1",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector is active")
	}
}

func TestExplainQueryHandler_AnalyzeFalse(t *testing.T) {
	var capturedQuery string
	registry := connector.NewRegistry()
	registry.Register(&capturingMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"QUERY PLAN"},
			Rows:     [][]any{{"Index Scan on users  (cost=0.00..1.05 rows=5 width=36)"}},
			RowCount: 1,
		},
		captureQuery: &capturedQuery,
	})

	handler := explainQueryHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"query":   "SELECT * FROM users",
		"analyze": false,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	// Without analyze, should not have ANALYZE in the explain prefix.
	if contains(capturedQuery, "ANALYZE") {
		t.Errorf("expected no ANALYZE in query, got: %s", capturedQuery)
	}
	if !contains(capturedQuery, "EXPLAIN") {
		t.Errorf("expected EXPLAIN in query, got: %s", capturedQuery)
	}
}

func TestExplainQueryHandler_FormatJSON(t *testing.T) {
	var capturedQuery string
	registry := connector.NewRegistry()
	registry.Register(&capturingMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"QUERY PLAN"},
			Rows:     [][]any{{`[{"Plan": {"Node Type": "Seq Scan"}}]`}},
			RowCount: 1,
		},
		captureQuery: &capturedQuery,
	})

	handler := explainQueryHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"query":  "SELECT * FROM users",
		"format": "json",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	if !contains(capturedQuery, "FORMAT JSON") {
		t.Errorf("expected FORMAT JSON in query, got: %s", capturedQuery)
	}
}

func TestExplainQueryHandler_QueryError(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&errorMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            errors.New("relation does not exist"),
	})

	handler := explainQueryHandler(registry)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"query": "SELECT * FROM nonexistent_table",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for bad query")
	}
	text := resultText(t, result)
	if !contains(text, "EXPLAIN failed") {
		t.Errorf("error should mention EXPLAIN failed, got: %s", text)
	}
}

func TestAnalyzeExplainPlan_SeqScan(t *testing.T) {
	plan := "Seq Scan on orders (cost=0.00..1234.00 rows=50000 width=36)"
	warnings := analyzeExplainPlan(plan)
	if len(warnings) == 0 {
		t.Error("expected warning for seq scan")
	}
}

func TestAnalyzeExplainPlan_NoWarnings(t *testing.T) {
	plan := "Index Scan using idx_users_id on users (cost=0.00..8.27 rows=1 width=36)"
	warnings := analyzeExplainPlan(plan)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

// capturingMockQE captures the query string sent to ExecuteReadQuery.
type capturingMockQE struct {
	mockDataSource
	result       *connector.QueryResult
	captureQuery *string
}

func (m *capturingMockQE) ExecuteReadQuery(ctx context.Context, query string) (*connector.QueryResult, error) {
	if m.captureQuery != nil {
		*m.captureQuery = query
	}
	return m.result, nil
}

// errorMockQE always returns an error from ExecuteReadQuery.
type errorMockQE struct {
	mockDataSource
	err error
}

func (m *errorMockQE) ExecuteReadQuery(ctx context.Context, query string) (*connector.QueryResult, error) {
	return nil, m.err
}
