package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/adham90/opentrace/internal/store"
)

func TestLogSearchHandler_Empty(t *testing.T) {
	ls := &mockLogStore{}
	handler := logSearchHandler(ls)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"query": "something",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "No log entries found") {
		t.Errorf("expected 'No log entries found' hint, got: %s", text)
	}
}

func TestLogSearchHandler_WithResults(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Timestamp: now.Add(-5 * time.Minute), Level: "error", Service: "api", Message: "connection refused"},
			{ID: 2, Timestamp: now.Add(-3 * time.Minute), Level: "error", Service: "api", Message: "connection timeout"},
			{ID: 3, Timestamp: now.Add(-1 * time.Minute), Level: "info", Service: "web", Message: "request completed"},
		},
	}
	handler := logSearchHandler(ls)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"level": "error",
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

	returned := resp["total_returned"].(float64)
	if returned != 2 {
		t.Errorf("total_returned = %v, want 2", returned)
	}

	entries := resp["entries"].([]any)
	if len(entries) != 2 {
		t.Errorf("entries count = %d, want 2", len(entries))
	}
}

func TestLogSearchHandler_WithTimeRange(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Timestamp: now.Add(-5 * time.Minute), Level: "error", Service: "api", Message: "recent error"},
			{ID: 2, Timestamp: now.Add(-2 * time.Hour), Level: "error", Service: "api", Message: "old error"},
		},
	}
	handler := logSearchHandler(ls)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"level":      "error",
		"time_range": "30m",
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

	returned := resp["total_returned"].(float64)
	if returned != 1 {
		t.Errorf("total_returned = %v, want 1 (only recent entry)", returned)
	}
}

func TestLogSearchHandler_InvalidTimeRange(t *testing.T) {
	ls := &mockLogStore{}
	handler := logSearchHandler(ls)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"time_range": "invalid",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid time_range")
	}

	text := resultText(t, result)
	if !contains(text, "invalid time_range") {
		t.Errorf("expected 'invalid time_range' error, got: %s", text)
	}
}

func TestLogSearchHandler_Pagination(t *testing.T) {
	now := time.Now().UTC()
	var entries []store.LogEntry
	for i := 0; i < 60; i++ {
		entries = append(entries, store.LogEntry{
			ID:        int64(i),
			Timestamp: now.Add(-time.Duration(i) * time.Minute),
			Level:     "info",
			Service:   "api",
			Message:   "entry",
		})
	}

	ls := &mockLogStore{entries: entries}
	handler := logSearchHandler(ls)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"limit": float64(50),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["has_more"] != true {
		t.Error("expected has_more=true when results hit limit")
	}
	if resp["next_offset"] != float64(50) {
		t.Errorf("next_offset = %v, want 50", resp["next_offset"])
	}
}

func TestLogSearchHandler_SortAsc(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Timestamp: now.Add(-5 * time.Minute), Level: "info", Service: "api", Message: "first"},
			{ID: 2, Timestamp: now.Add(-3 * time.Minute), Level: "info", Service: "api", Message: "second"},
			{ID: 3, Timestamp: now.Add(-1 * time.Minute), Level: "info", Service: "api", Message: "third"},
		},
	}
	handler := logSearchHandler(ls)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"sort": "asc",
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

	entries := resp["entries"].([]any)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// Entries should be in the order returned by the store (mock doesn't reorder, but param is passed).
	if resp["total_returned"].(float64) != 3 {
		t.Errorf("total_returned = %v, want 3", resp["total_returned"])
	}
}

func TestLogSearchHandler_FieldsProjection(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Timestamp: now.Add(-5 * time.Minute), Level: "error", Service: "api",
				Message: "test error", Environment: "production",
				Metadata: map[string]any{"host": "server-01"}},
		},
	}
	handler := logSearchHandler(ls)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"fields": "id,level,message",
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

	entries := resp["entries"].([]any)
	entry := entries[0].(map[string]any)

	// Should have id, level, message.
	if _, ok := entry["id"]; !ok {
		t.Error("expected 'id' in projected fields")
	}
	if _, ok := entry["level"]; !ok {
		t.Error("expected 'level' in projected fields")
	}
	if _, ok := entry["message"]; !ok {
		t.Error("expected 'message' in projected fields")
	}
	// Should NOT have service, environment, metadata, timestamp.
	if _, ok := entry["service"]; ok {
		t.Error("did not expect 'service' in projected fields")
	}
	if _, ok := entry["environment"]; ok {
		t.Error("did not expect 'environment' in projected fields")
	}
	if _, ok := entry["metadata"]; ok {
		t.Error("did not expect 'metadata' in projected fields")
	}
	if _, ok := entry["timestamp"]; ok {
		t.Error("did not expect 'timestamp' in projected fields")
	}
}

func TestLogSearchHandler_TimeHistogram(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Timestamp: now.Add(-4 * time.Minute), Level: "info", Service: "api", Message: "a"},
			{ID: 2, Timestamp: now.Add(-3 * time.Minute), Level: "info", Service: "api", Message: "b"},
			{ID: 3, Timestamp: now.Add(-1 * time.Minute), Level: "info", Service: "api", Message: "c"},
		},
	}
	handler := logSearchHandler(ls)

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

	dist, ok := resp["time_distribution"].(map[string]any)
	if !ok {
		t.Fatal("expected time_distribution field")
	}
	if _, ok := dist["bucket_size"]; !ok {
		t.Error("expected bucket_size in time_distribution")
	}
	buckets, ok := dist["buckets"].(map[string]any)
	if !ok {
		t.Fatal("expected buckets map in time_distribution")
	}
	if len(buckets) == 0 {
		t.Error("expected at least one bucket")
	}
}

func TestLogSearchHandler_ExpandedSearch(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Timestamp: now.Add(-5 * time.Minute), Level: "info", Service: "payment-api", Message: "processed"},
			{ID: 2, Timestamp: now.Add(-3 * time.Minute), Level: "info", Service: "web", Message: "served"},
		},
	}
	handler := logSearchHandler(ls)

	// Search for "payment-api" as a query — should fall back to matching as service name.
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"query": "payment-api",
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

	if resp["total_returned"].(float64) != 1 {
		t.Errorf("total_returned = %v, want 1 (fallback to service match)", resp["total_returned"])
	}
}

func TestLogSearchHandler_Summary(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Timestamp: now.Add(-5 * time.Minute), Level: "error", Service: "api", Message: "fail"},
			{ID: 2, Timestamp: now.Add(-3 * time.Minute), Level: "info", Service: "web", Message: "ok"},
		},
	}
	handler := logSearchHandler(ls)

	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	summary, ok := resp["summary"].(map[string]any)
	if !ok {
		t.Fatal("expected summary field")
	}
	byLevel, ok := summary["by_level"].(map[string]any)
	if !ok {
		t.Fatal("expected by_level in summary")
	}
	if byLevel["error"].(float64) != 1 {
		t.Errorf("by_level[error] = %v, want 1", byLevel["error"])
	}
	if byLevel["info"].(float64) != 1 {
		t.Errorf("by_level[info] = %v, want 1", byLevel["info"])
	}
}

// --- helper to make CallToolRequest from server_test.go ---

func makeLogSearchRequest(args map[string]any) mcp.CallToolRequest {
	return makeRequest(args)
}
