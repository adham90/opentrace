package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

func TestTraceLookupHandler_MultiService(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{TraceID: "trace-1", Service: "api-gateway", Level: "info", Message: "POST /api/orders", Timestamp: now},
			{TraceID: "trace-1", Service: "web-api", Level: "info", Message: "Processing order", Timestamp: now.Add(50 * time.Millisecond)},
			{TraceID: "trace-1", Service: "user-service", Level: "error", Message: "Connection refused", Timestamp: now.Add(1200 * time.Millisecond)},
			{TraceID: "trace-1", Service: "web-api", Level: "error", Message: "Upstream service error", Timestamp: now.Add(2340 * time.Millisecond)},
		},
	}

	handler := traceLookupHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"trace_id": "trace-1",
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

	if resp["total_entries"].(float64) != 4 {
		t.Errorf("total_entries = %v, want 4", resp["total_entries"])
	}
	if !resp["has_errors"].(bool) {
		t.Error("expected has_errors = true")
	}

	services, ok := resp["services_touched"].([]any)
	if !ok {
		t.Fatal("expected services_touched")
	}
	if len(services) != 3 {
		t.Errorf("expected 3 services, got %d", len(services))
	}

	// Timeline should be sorted by timestamp.
	timeline, ok := resp["timeline"].([]any)
	if !ok || len(timeline) != 4 {
		t.Fatalf("expected 4 timeline entries, got %v", len(timeline))
	}
	first := timeline[0].(map[string]any)
	if first["elapsed_ms"].(float64) != 0 {
		t.Errorf("first entry elapsed_ms = %v, want 0", first["elapsed_ms"])
	}
	last := timeline[3].(map[string]any)
	if last["elapsed_ms"].(float64) != 2340 {
		t.Errorf("last entry elapsed_ms = %v, want 2340", last["elapsed_ms"])
	}

	// Check service summary.
	summary, ok := resp["service_summary"].([]any)
	if !ok {
		t.Fatal("expected service_summary")
	}
	if len(summary) != 3 {
		t.Errorf("expected 3 service summaries, got %d", len(summary))
	}
}

func TestTraceLookupHandler_NoEntries(t *testing.T) {
	ls := &mockLogStore{}

	handler := traceLookupHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"trace_id": "nonexistent-trace",
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

	if resp["total_entries"].(float64) != 0 {
		t.Errorf("total_entries = %v, want 0", resp["total_entries"])
	}
}

func TestTraceLookupHandler_MissingTraceID(t *testing.T) {
	ls := &mockLogStore{}

	handler := traceLookupHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing trace_id")
	}
}

func TestTraceLookupHandler_ErrorRootCause(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{TraceID: "trace-2", Service: "web", Level: "info", Message: "Start", Timestamp: now},
			{TraceID: "trace-2", Service: "db", Level: "error", Message: "Timeout", Timestamp: now.Add(500 * time.Millisecond)},
			{TraceID: "trace-2", Service: "web", Level: "error", Message: "Failed", Timestamp: now.Add(600 * time.Millisecond)},
		},
	}

	handler := traceLookupHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"trace_id": "trace-2",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	warnings, ok := resp["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatal("expected root cause warning")
	}
	// First warning should point to db error.
	w := warnings[0].(string)
	if !contains(w, "db") {
		t.Errorf("expected root cause warning to mention 'db', got: %s", w)
	}
}

func TestTraceLookupHandler_GapDetection(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{TraceID: "trace-3", Service: "api", Level: "info", Message: "Start", Timestamp: now},
			{TraceID: "trace-3", Service: "worker", Level: "info", Message: "Processing", Timestamp: now.Add(2 * time.Second)},
		},
	}

	handler := traceLookupHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"trace_id": "trace-3",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	warnings, ok := resp["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatal("expected gap warning")
	}
	found := false
	for _, w := range warnings {
		if contains(w.(string), "Gap") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected gap detection warning")
	}
}

func TestTraceLookupHandler_SingleService(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{TraceID: "trace-4", Service: "web", Level: "info", Message: "Request start", Timestamp: now},
			{TraceID: "trace-4", Service: "web", Level: "info", Message: "Request end", Timestamp: now.Add(100 * time.Millisecond)},
		},
	}

	handler := traceLookupHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"trace_id": "trace-4",
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

	if resp["total_entries"].(float64) != 2 {
		t.Errorf("total_entries = %v, want 2", resp["total_entries"])
	}
	services := resp["services_touched"].([]any)
	if len(services) != 1 {
		t.Errorf("expected 1 service, got %d", len(services))
	}
}

func TestTraceLookupHandler_WithContext(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{TraceID: "trace-5", Service: "web", Level: "info", Message: "Trace entry", Timestamp: now},
			{TraceID: "", Service: "web", Level: "info", Message: "Context entry", Timestamp: now.Add(500 * time.Millisecond)},
		},
	}

	handler := traceLookupHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"trace_id":        "trace-5",
		"include_context": true,
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

	// Should have context_entries field.
	ctx, ok := resp["context_entries"].([]any)
	if !ok {
		t.Fatal("expected context_entries field")
	}
	// The context entry (no trace_id) should appear.
	if len(ctx) == 0 {
		t.Error("expected at least 1 context entry")
	}
}
