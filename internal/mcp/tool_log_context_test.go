package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

func TestLogContextHandler_Success(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Timestamp: now.Add(-5 * time.Minute), Level: "info", Service: "api", Message: "request started"},
			{ID: 2, Timestamp: now.Add(-4 * time.Minute), Level: "info", Service: "api", Message: "processing"},
			{ID: 3, Timestamp: now.Add(-3 * time.Minute), Level: "error", Service: "api", Message: "database timeout"},
			{ID: 4, Timestamp: now.Add(-2 * time.Minute), Level: "info", Service: "api", Message: "retrying"},
			{ID: 5, Timestamp: now.Add(-1 * time.Minute), Level: "info", Service: "worker", Message: "job complete"},
		},
	}

	handler := logContextHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"log_id": float64(3),
		"before": float64(2),
		"after":  float64(2),
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

	if resp["anchor_id"] != float64(3) {
		t.Errorf("anchor_id = %v, want 3", resp["anchor_id"])
	}

	entries, ok := resp["entries"].([]any)
	if !ok {
		t.Fatal("expected entries array")
	}
	// Should have at least the anchor entry.
	if len(entries) < 1 {
		t.Error("expected at least 1 entry (the anchor)")
	}

	// Find the anchor entry.
	foundAnchor := false
	for _, e := range entries {
		entry := e.(map[string]any)
		if entry["position"] == "anchor" {
			foundAnchor = true
			if !contains(entry["message"].(string), "database timeout") {
				t.Errorf("anchor message = %v, want 'database timeout'", entry["message"])
			}
		}
	}
	if !foundAnchor {
		t.Error("expected to find anchor entry")
	}
}

func TestLogContextHandler_SameService(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Timestamp: now.Add(-3 * time.Minute), Level: "info", Service: "api", Message: "api log"},
			{ID: 2, Timestamp: now.Add(-2 * time.Minute), Level: "info", Service: "worker", Message: "worker log"},
			{ID: 3, Timestamp: now.Add(-1 * time.Minute), Level: "error", Service: "api", Message: "anchor"},
		},
	}

	handler := logContextHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"log_id":       float64(3),
		"before":       float64(5),
		"after":        float64(5),
		"same_service": true,
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	if resp["same_service"] != true {
		t.Errorf("same_service = %v, want true", resp["same_service"])
	}
}

func TestLogContextHandler_NotFound(t *testing.T) {
	ls := &mockLogStore{entries: []store.LogEntry{}}
	handler := logContextHandler(ls)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"log_id": float64(999),
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when log not found")
	}
}

func TestLogContextHandler_MissingLogID(t *testing.T) {
	ls := &mockLogStore{}
	handler := logContextHandler(ls)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when log_id is missing")
	}
}
