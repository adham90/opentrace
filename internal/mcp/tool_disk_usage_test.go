package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestDiskUsageHandler_Success(t *testing.T) {
	registry := connector.NewRegistry()

	callCount := 0
	registry.Register(&multiQueryMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		handler: func(query string) (*connector.QueryResult, error) {
			callCount++
			if callCount == 1 {
				// Database summary query.
				return &connector.QueryResult{
					Columns:  []string{"database", "total_size", "total_bytes"},
					Rows:     [][]any{{"mydb", "1 GB", int64(1073741824)}},
					RowCount: 1,
				}, nil
			}
			// Table breakdown query.
			return &connector.QueryResult{
				Columns: []string{"table_name", "total_size", "total_bytes", "table_size", "table_bytes", "index_size", "index_bytes", "toast_size", "toast_bytes"},
				Rows: [][]any{
					{"public.users", "500 MB", int64(524288000), "300 MB", int64(314572800), "180 MB", int64(188743680), "20 MB", int64(20971520)},
					{"public.orders", "200 MB", int64(209715200), "150 MB", int64(157286400), "50 MB", int64(52428800), "0 bytes", int64(0)},
				},
				RowCount: 2,
			}, nil
		},
	})

	handler := diskUsageHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "public.users") {
		t.Errorf("expected users table in output, got: %s", text)
	}
	if !contains(text, "1 GB") {
		t.Errorf("expected database size in output, got: %s", text)
	}
}

func TestDiskUsageHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := diskUsageHandler(registry)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector")
	}
}

func TestDiskUsageHandler_QueryError(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            errors.New("permission denied"),
	})

	handler := diskUsageHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}

// multiQueryMockQE allows different results for sequential queries.
type multiQueryMockQE struct {
	mockDataSource
	handler func(query string) (*connector.QueryResult, error)
}

func (m *multiQueryMockQE) ExecuteReadQuery(ctx context.Context, query string) (*connector.QueryResult, error) {
	return m.handler(query)
}
