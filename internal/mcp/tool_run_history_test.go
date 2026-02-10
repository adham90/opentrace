package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

func TestMonitorRunHistoryHandler_Success(t *testing.T) {
	monitorID := uuid.New()
	now := time.Now().UTC()
	finished := now.Add(-1 * time.Minute)

	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: monitorID, Title: "Connection Monitor", Status: store.WatcherActive, MonitorType: store.MonitorTypeRule, Schedule: "*/5 * * * *", TimeRange: "5m"},
		},
	}
	rs := &mockWatcherRunStore{
		runs: []store.WatcherRun{
			{ID: uuid.New(), WatcherID: monitorID, StartedAt: now.Add(-2 * time.Minute), FinishedAt: &finished, Status: "completed", HasAlert: true},
			{ID: uuid.New(), WatcherID: monitorID, StartedAt: now.Add(-7 * time.Minute), FinishedAt: &finished, Status: "completed", HasAlert: false},
		},
	}

	handler := monitorRunHistoryHandler(ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"monitor_id": monitorID.String(),
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

	monitor, ok := resp["monitor"].(map[string]any)
	if !ok {
		t.Fatal("expected monitor field")
	}
	if monitor["title"] != "Connection Monitor" {
		t.Errorf("monitor title = %v, want Connection Monitor", monitor["title"])
	}

	runs, ok := resp["runs"].([]any)
	if !ok {
		t.Fatal("expected runs array")
	}
	if len(runs) != 2 {
		t.Errorf("expected 2 runs, got %d", len(runs))
	}
}

func TestMonitorRunHistoryHandler_InvalidMonitorID(t *testing.T) {
	ws := &mockWatcherStore{}
	rs := &mockWatcherRunStore{}

	handler := monitorRunHistoryHandler(ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"monitor_id": "not-a-uuid",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid monitor_id")
	}
}

func TestMonitorRunHistoryHandler_MonitorNotFound(t *testing.T) {
	ws := &mockWatcherStore{}
	rs := &mockWatcherRunStore{}

	handler := monitorRunHistoryHandler(ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"monitor_id": uuid.New().String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for non-existent monitor")
	}
}

func TestMonitorRunHistoryHandler_EmptyRuns(t *testing.T) {
	monitorID := uuid.New()
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: monitorID, Title: "Test Monitor", Status: store.WatcherActive},
		},
	}
	rs := &mockWatcherRunStore{}

	handler := monitorRunHistoryHandler(ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"monitor_id": monitorID.String(),
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

	runs, ok := resp["runs"].([]any)
	if !ok {
		t.Fatal("expected runs array")
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestMonitorRunHistoryHandler_StatusFilter(t *testing.T) {
	monitorID := uuid.New()
	now := time.Now().UTC()
	finished := now.Add(-1 * time.Minute)

	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: monitorID, Title: "Test", Status: store.WatcherActive},
		},
	}
	rs := &mockWatcherRunStore{
		runs: []store.WatcherRun{
			{ID: uuid.New(), WatcherID: monitorID, StartedAt: now, FinishedAt: &finished, Status: "completed", HasAlert: false},
			{ID: uuid.New(), WatcherID: monitorID, StartedAt: now, FinishedAt: &finished, Status: "error", HasAlert: false},
			{ID: uuid.New(), WatcherID: monitorID, StartedAt: now, FinishedAt: &finished, Status: "completed", HasAlert: true},
		},
	}

	handler := monitorRunHistoryHandler(ws, rs)

	// Filter for alerted runs only.
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"monitor_id":    monitorID.String(),
		"status_filter": "alerted",
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

	runs, ok := resp["runs"].([]any)
	if !ok {
		t.Fatal("expected runs array")
	}
	if len(runs) != 1 {
		t.Errorf("expected 1 alerted run, got %d", len(runs))
	}
}

func TestMonitorRunHistoryHandler_MissingMonitorID(t *testing.T) {
	ws := &mockWatcherStore{}
	rs := &mockWatcherRunStore{}

	handler := monitorRunHistoryHandler(ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing monitor_id")
	}
}
