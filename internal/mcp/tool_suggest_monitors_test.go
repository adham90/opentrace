package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

func TestSuggestMonitorsHandler_NoExisting(t *testing.T) {
	now := time.Now().UTC()
	ws := &mockWatcherStore{}
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.10", Timestamp: now.Add(-10 * time.Minute)},
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.20", Timestamp: now.Add(-9 * time.Minute)},
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.10", Timestamp: now.Add(-8 * time.Minute)},
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.10", Timestamp: now.Add(-7 * time.Minute)},
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.10", Timestamp: now.Add(-6 * time.Minute)},
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.10", Timestamp: now.Add(-5 * time.Minute)},
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.10", Timestamp: now.Add(-4 * time.Minute)},
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.10", Timestamp: now.Add(-3 * time.Minute)},
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.10", Timestamp: now.Add(-2 * time.Minute)},
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.10", Timestamp: now.Add(-1 * time.Minute)},
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.10", Timestamp: now.Add(-30 * time.Second)},
			{Level: "info", Service: "web", Message: "ok", Timestamp: now.Add(-15 * time.Minute)},
		},
	}

	handler := suggestMonitorsHandler(ws, ls)
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

	suggestions, ok := resp["suggestions"].([]any)
	if !ok {
		t.Fatal("expected suggestions field")
	}
	if len(suggestions) == 0 {
		t.Error("expected at least 1 suggestion with no existing monitors and errors")
	}

	// Should have gaps.
	gaps, _ := resp["gaps"].([]any)
	if len(gaps) == 0 {
		t.Error("expected monitoring gaps")
	}
}

func TestSuggestMonitorsHandler_FullCoverage(t *testing.T) {
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{Title: "Slow Query Monitor", Status: store.WatcherActive},
			{Title: "Error Rate Alert", Status: store.WatcherActive},
			{Title: "Connection Pool Health", Status: store.WatcherActive},
			{Title: "Replication Lag", Status: store.WatcherActive},
			{Title: "Vacuum Status", Status: store.WatcherActive},
			{Title: "Disk Usage", Status: store.WatcherActive},
			{Title: "Auth Failure Detection", Status: store.WatcherActive},
			{Title: "Login Rate Monitor", Status: store.WatcherActive},
		},
	}
	ls := &mockLogStore{}

	handler := suggestMonitorsHandler(ws, ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	coverage := resp["existing_coverage"].(map[string]any)
	if coverage["total_monitors"].(float64) != 8 {
		t.Errorf("total_monitors = %v, want 8", coverage["total_monitors"])
	}

	// With full coverage, suggestions should be minimal.
	suggestions, _ := resp["suggestions"].([]any)
	// No critical gaps should remain.
	if len(suggestions) > 3 {
		t.Errorf("expected minimal suggestions with full coverage, got %d", len(suggestions))
	}
}

func TestSuggestMonitorsHandler_FocusPerformance(t *testing.T) {
	ws := &mockWatcherStore{}
	ls := &mockLogStore{}

	handler := suggestMonitorsHandler(ws, ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"focus": "performance",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	suggestions, _ := resp["suggestions"].([]any)
	for _, s := range suggestions {
		entry := s.(map[string]any)
		if entry["category"] != "performance" {
			t.Errorf("expected all suggestions to be performance, got %v", entry["category"])
		}
	}
}

func TestSuggestMonitorsHandler_NilStores(t *testing.T) {
	handler := suggestMonitorsHandler(nil, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success even with nil stores, got error: %s", resultText(t, result))
	}
}
