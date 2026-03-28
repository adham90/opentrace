package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

func TestLogsHandler_UnknownAction(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}
	handler := LogsHandler(deps)

	req := MakeCallToolRequest("logs", map[string]any{"action": "nonexistent"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true for unknown action")
	}
}

func TestLogsHandler_MissingAction(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}
	handler := LogsHandler(deps)

	req := MakeCallToolRequest("logs", map[string]any{})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when action is missing")
	}
}

func TestLogsSearch_EmptyStore(t *testing.T) {
	ls := mocks.NewLogStore()
	deps := LogsDeps{
		LogStore:        ls,
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "search", "query": "test"}
	result, err := LogsSearch(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for empty store")
	}
}

func TestLogsSearch_WithEntries(t *testing.T) {
	ls := mocks.NewLogStore()
	now := time.Now().UTC()
	ls.Entries = []store.LogEntry{
		{
			ID:        1,
			Timestamp: now,
			Level:     "info",
			Service:   "api",
			Message:   "request handled",
		},
		{
			ID:        2,
			Timestamp: now.Add(-time.Minute),
			Level:     "error",
			Service:   "api",
			Message:   "something went wrong",
		},
	}

	deps := LogsDeps{
		LogStore:        ls,
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "search", "service": "api"}
	result, err := LogsSearch(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	// Parse response JSON to verify entries are returned.
	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	total, ok := resp["total_returned"].(float64)
	if !ok {
		t.Fatalf("expected total_returned in response, got keys: %v", keysOf(resp))
	}
	if int(total) != 2 {
		t.Errorf("expected total_returned=2, got %v", total)
	}
}

func TestLogsSearch_StoreError(t *testing.T) {
	ls := mocks.NewLogStore()
	ls.Err = errors.New("database connection failed")

	deps := LogsDeps{
		LogStore:        ls,
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "search", "query": "test"}
	result, err := LogsSearch(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when store returns error")
	}
}

func TestLogsContext_MissingLogID(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "context"}
	result, err := LogsContext(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when log_id is missing")
	}
}

func TestLogsContext_InvalidLogID(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "context", "log_id": float64(-1)}
	result, err := LogsContext(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when log_id is invalid")
	}
}

func TestLogsAttributes_Empty(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	// field is required — with a valid field but no data, returns empty list text.
	args := map[string]any{"action": "attributes", "field": "service"}
	result, err := LogsAttributes(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for empty attributes")
	}
}

func TestLogsAttributes_MissingField(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "attributes"}
	result, err := LogsAttributes(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when field is missing")
	}
}

func TestLogsStats_NoLogs(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "stats", "time_range": "1h", "group_by": "level"}
	result, err := LogsStats(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for empty stats")
	}

	// Verify the response parses and has expected fields.
	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if _, ok := resp["total_logs"]; !ok {
		t.Error("expected total_logs field in stats response")
	}
}

func TestLogsSummary_NoLogs(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "summary", "time_range": "1h"}
	result, err := LogsSummary(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for empty summary")
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if _, ok := resp["total_logs"]; !ok {
		t.Error("expected total_logs field in summary response")
	}
}

// keysOf returns the keys of a map for diagnostic messages.
func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
