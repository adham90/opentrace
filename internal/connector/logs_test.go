package connector

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type mockLogStore struct {
	entries []store.LogEntry
	err     error
}

func (m *mockLogStore) BatchInsert(ctx context.Context, entries []store.LogEntry) (int, error) {
	return len(entries), m.err
}

func (m *mockLogStore) Search(ctx context.Context, params store.LogSearchParams) ([]store.LogEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.entries, nil
}

func (m *mockLogStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
}

func (m *mockLogStore) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) {
	return nil, nil
}

func (m *mockLogStore) Histogram(_ context.Context, _ store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	return nil, nil
}

func (m *mockLogStore) CountByService(_ context.Context, _ store.LogCountParams) ([]store.ServiceLogCount, error) {
	return nil, nil
}

func (m *mockLogStore) DistinctValues(_ context.Context, _ string, _ store.LogCountParams) ([]string, error) {
	return nil, nil
}

func (m *mockLogStore) MetadataKeys(_ context.Context, _ store.LogCountParams) ([]string, error) {
	return nil, nil
}

func (m *mockLogStore) GetByID(_ context.Context, _ int64) (*store.LogEntry, error) {
	return nil, store.ErrNotFound
}

func (m *mockLogStore) SearchRequestSummaries(_ context.Context, _ store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	return nil, nil
}
func (m *mockLogStore) RecordBatch(_ context.Context, _ string, _ int) error { return nil }
func (m *mockLogStore) GetBatch(_ context.Context, _ string) (*store.BatchRecord, error) { return nil, nil }
func (m *mockLogStore) PruneBatches(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

func TestLogsConnector_TestConnection(t *testing.T) {
	c := NewLogsConnector(&mockLogStore{})
	err := c.TestConnection(context.Background())
	if err != nil {
		t.Fatalf("TestConnection() returned unexpected error: %v", err)
	}
}

func TestLogsConnector_TestConnection_Error(t *testing.T) {
	ms := &mockLogStore{err: fmt.Errorf("database unavailable")}
	c := NewLogsConnector(ms)
	err := c.TestConnection(context.Background())
	if err == nil {
		t.Fatal("TestConnection() should return error when store fails")
	}
}

func TestLogsConnector_Close(t *testing.T) {
	c := NewLogsConnector(&mockLogStore{})
	err := c.Close()
	if err != nil {
		t.Fatalf("Close() returned unexpected error: %v", err)
	}
}

func TestLogsConnector_Type(t *testing.T) {
	c := NewLogsConnector(&mockLogStore{})
	if c.Type() != ConnectorLogs {
		t.Fatalf("Type() = %q, want %q", c.Type(), ConnectorLogs)
	}
}

func TestLogsConnector_Tools(t *testing.T) {
	c := NewLogsConnector(&mockLogStore{})
	tools := c.Tools()
	if len(tools) != 1 {
		t.Fatalf("len(Tools()) = %d, want 1", len(tools))
	}
	if tools[0].Name != "log_search" {
		t.Fatalf("tool name = %q, want %q", tools[0].Name, "log_search")
	}
}

func TestLogSearchTool_Success(t *testing.T) {
	ms := &mockLogStore{
		entries: []store.LogEntry{
			{
				Timestamp: time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC),
				Level:     "ERROR",
				Service:   "api",
				Message:   "database connection timeout",
			},
			{
				Timestamp: time.Date(2024, 1, 1, 10, 1, 0, 0, time.UTC),
				Level:     "INFO",
				Service:   "api",
				Message:   "retrying connection",
			},
		},
	}
	c := NewLogsConnector(ms)
	tools := c.Tools()

	result, err := tools[0].Handler(context.Background(), map[string]any{
		"query": "connection",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !contains(result, "Found 2 log entries") {
		t.Errorf("result missing count header: %s", result)
	}
	if !contains(result, "database connection timeout") {
		t.Errorf("result missing first entry: %s", result)
	}
}

func TestLogSearchTool_NoResults(t *testing.T) {
	ms := &mockLogStore{entries: []store.LogEntry{}}
	c := NewLogsConnector(ms)
	tools := c.Tools()

	result, err := tools[0].Handler(context.Background(), map[string]any{
		"query": "nonexistent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "No logs found") {
		t.Errorf("expected 'No logs found' message, got: %s", result)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
