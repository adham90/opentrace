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

// --- helper to make CallToolRequest from server_test.go ---

func makeLogSearchRequest(args map[string]any) mcp.CallToolRequest {
	return makeRequest(args)
}
