package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

func TestListLogAttributesHandler_Services(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Timestamp: now.Add(-1 * time.Hour), Level: "error", Service: "api"},
			{ID: 2, Timestamp: now.Add(-2 * time.Hour), Level: "info", Service: "worker"},
			{ID: 3, Timestamp: now.Add(-3 * time.Hour), Level: "warn", Service: "api"},
		},
	}

	handler := listLogAttributesHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"field":      "service",
		"time_range": "24h",
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

	if resp["field"] != "service" {
		t.Errorf("field = %v, want service", resp["field"])
	}
	if resp["count"].(float64) < 1 {
		t.Error("expected at least 1 service value")
	}
}

func TestListLogAttributesHandler_MetadataKeys(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Timestamp: now.Add(-1 * time.Hour), Level: "error", Service: "api",
				Metadata: map[string]any{"host": "server-01", "region": "us-east"}},
			{ID: 2, Timestamp: now.Add(-2 * time.Hour), Level: "info", Service: "worker",
				Metadata: map[string]any{"host": "server-02", "request_id": "abc123"}},
		},
	}

	handler := listLogAttributesHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"field":      "metadata_key",
		"time_range": "24h",
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

	if resp["field"] != "metadata_key" {
		t.Errorf("field = %v, want metadata_key", resp["field"])
	}
	// Should find host, region, request_id.
	if resp["count"].(float64) < 2 {
		t.Error("expected at least 2 metadata keys")
	}
	if _, ok := resp["hint"]; !ok {
		t.Error("expected hint for metadata keys")
	}
}

func TestListLogAttributesHandler_MissingField(t *testing.T) {
	ls := &mockLogStore{}
	handler := listLogAttributesHandler(ls)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when field is missing")
	}
}

func TestListLogAttributesHandler_EmptyResults(t *testing.T) {
	ls := &mockLogStore{entries: []store.LogEntry{}}
	handler := listLogAttributesHandler(ls)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"field":      "service",
		"time_range": "24h",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success even with empty results")
	}

	text := resultText(t, result)
	if !contains(text, "No service values found") {
		t.Errorf("expected 'No service values found' message, got: %s", text)
	}
}
