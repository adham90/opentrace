package connector

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
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
