package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

func TestIncidentTimelineHandler_Success(t *testing.T) {
	now := time.Now().UTC()

	ls := &mockLogStore{
		entries: []store.LogEntry{
			{
				ID:        1,
				Timestamp: now.Add(-25 * time.Minute),
				Level:     "error",
				Service:   "api",
				Message:   "connection pool exhausted",
			},
		},
	}

	handler := incidentTimelineHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"start": now.Add(-1 * time.Hour).Format(time.RFC3339),
		"end":   now.Format(time.RFC3339),
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

	totalEvents, ok := resp["total_events"].(float64)
	if !ok || totalEvents < 1 {
		t.Errorf("expected at least 1 event, got: %v", resp["total_events"])
	}
}

func TestIncidentTimelineHandler_MissingStart(t *testing.T) {
	ls := &mockLogStore{}
	handler := incidentTimelineHandler(ls)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"end": time.Now().Format(time.RFC3339),
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing start")
	}
}

func TestIncidentTimelineHandler_MissingEnd(t *testing.T) {
	ls := &mockLogStore{}
	handler := incidentTimelineHandler(ls)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"start": time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing end")
	}
}

func TestIncidentTimelineHandler_EndBeforeStart(t *testing.T) {
	ls := &mockLogStore{}
	handler := incidentTimelineHandler(ls)

	now := time.Now()
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"start": now.Format(time.RFC3339),
		"end":   now.Add(-1 * time.Hour).Format(time.RFC3339),
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when end is before start")
	}
}

func TestIncidentTimelineHandler_Empty(t *testing.T) {
	ls := &mockLogStore{}
	handler := incidentTimelineHandler(ls)

	now := time.Now()
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"start": now.Add(-1 * time.Hour).Format(time.RFC3339),
		"end":   now.Format(time.RFC3339),
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success for empty timeline, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["total_events"] != float64(0) {
		t.Errorf("expected 0 events, got: %v", resp["total_events"])
	}
}
