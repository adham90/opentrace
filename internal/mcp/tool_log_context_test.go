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

func TestLogContextHandler_ReturnsMetadata(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{
				ID: 1, Timestamp: now.Add(-3 * time.Minute), Level: "info", Service: "api",
				Message:  "before entry",
				Metadata: map[string]any{"user_id": float64(42), "tenant": "acme"},
			},
			{
				ID: 2, Timestamp: now.Add(-2 * time.Minute), Level: "error", Service: "api",
				Message:  "the anchor error",
				Metadata: map[string]any{"user_id": float64(42), "request_id": "req-001"},
			},
			{
				ID: 3, Timestamp: now.Add(-1 * time.Minute), Level: "info", Service: "api",
				Message:  "after entry",
				Metadata: map[string]any{"hostname": "web-01"},
			},
		},
	}

	handler := logContextHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"log_id": float64(2),
		"before": float64(5),
		"after":  float64(5),
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

	entries, ok := resp["entries"].([]any)
	if !ok {
		t.Fatal("expected entries array")
	}

	// Find anchor and verify metadata
	for _, e := range entries {
		entry := e.(map[string]any)
		if entry["position"] == "anchor" {
			meta, ok := entry["metadata"].(map[string]any)
			if !ok {
				t.Fatal("anchor entry should have metadata")
			}
			if meta["user_id"].(float64) != 42 {
				t.Errorf("anchor metadata.user_id = %v, want 42", meta["user_id"])
			}
			if meta["request_id"] != "req-001" {
				t.Errorf("anchor metadata.request_id = %v, want req-001", meta["request_id"])
			}
		}
		if entry["position"] == "before" {
			meta, ok := entry["metadata"].(map[string]any)
			if !ok {
				t.Fatal("before entry should have metadata")
			}
			if meta["tenant"] != "acme" {
				t.Errorf("before metadata.tenant = %v, want acme", meta["tenant"])
			}
		}
	}
}

func TestLogContextHandler_BeforeAfterCounts(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Timestamp: now.Add(-5 * time.Minute), Level: "info", Service: "api", Message: "entry 1"},
			{ID: 2, Timestamp: now.Add(-4 * time.Minute), Level: "info", Service: "api", Message: "entry 2"},
			{ID: 3, Timestamp: now.Add(-3 * time.Minute), Level: "error", Service: "api", Message: "anchor entry"},
			{ID: 4, Timestamp: now.Add(-2 * time.Minute), Level: "info", Service: "api", Message: "entry 4"},
			{ID: 5, Timestamp: now.Add(-1 * time.Minute), Level: "info", Service: "api", Message: "entry 5"},
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

	beforeCount := resp["before_count"].(float64)
	afterCount := resp["after_count"].(float64)

	if beforeCount != 2 {
		t.Errorf("before_count = %v, want 2", beforeCount)
	}
	if afterCount != 2 {
		t.Errorf("after_count = %v, want 2", afterCount)
	}

	// Total entries should be before + anchor + after = 5
	entries := resp["entries"].([]any)
	if len(entries) != 5 {
		t.Errorf("total entries = %d, want 5 (2 before + 1 anchor + 2 after)", len(entries))
	}

	// Verify positions are in order
	positions := make([]string, len(entries))
	for i, e := range entries {
		positions[i] = e.(map[string]any)["position"].(string)
	}
	expected := []string{"before", "before", "anchor", "after", "after"}
	for i, p := range positions {
		if p != expected[i] {
			t.Errorf("position[%d] = %q, want %q", i, p, expected[i])
		}
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
