package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestLogsPerformance_Empty(t *testing.T) {
	ls := mocks.NewLogStore()
	deps := LogsDeps{
		LogStore:        ls,
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "performance"}
	result, err := LogsPerformance(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for empty performance data")
	}

	// The mock returns nil from SearchRequestSummaries, so we expect the "no data" hint.
	text := extractText(t, result)
	if !strings.Contains(text, "No request performance data found") {
		t.Errorf("expected 'No request performance data found' hint, got: %s", text)
	}
}

func TestLogsTrace_MissingTraceID(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	// Omit trace_id entirely.
	args := map[string]any{"action": "trace"}
	result, err := LogsTrace(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when trace_id is missing")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "trace_id is required") {
		t.Errorf("expected 'trace_id is required' in error text, got: %s", text)
	}
}

func TestLogsTrace_Empty(t *testing.T) {
	ls := mocks.NewLogStore()
	deps := LogsDeps{
		LogStore:        ls,
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "trace", "trace_id": "abc-123-def"}
	result, err := LogsTrace(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for empty trace")
	}

	text := extractText(t, result)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if parsed["trace_id"] != "abc-123-def" {
		t.Errorf("trace_id = %v, want %q", parsed["trace_id"], "abc-123-def")
	}
	if parsed["total_entries"] != float64(0) {
		t.Errorf("total_entries = %v, want 0", parsed["total_entries"])
	}
	if parsed["message"] != "No log entries found for this trace ID" {
		t.Errorf("unexpected message: %v", parsed["message"])
	}
}

func TestLogsCompare_MissingPeriod(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	// Provide a valid metric but an unsupported current_period to trigger the error.
	args := map[string]any{
		"action":         "compare",
		"metric":         "errors",
		"current_period": "invalid_period",
	}
	result, err := LogsCompare(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError for invalid current_period")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "invalid current_period") {
		t.Errorf("expected 'invalid current_period' in error text, got: %s", text)
	}
}

func TestLogsCompare_ValidPeriod(t *testing.T) {
	ls := mocks.NewLogStore()
	deps := LogsDeps{
		LogStore:        ls,
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	// Use valid defaults: metric=errors, current_period=last_1h (default), baseline=previous (default).
	args := map[string]any{
		"action": "compare",
		"metric": "errors",
	}
	result, err := LogsCompare(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		text := extractText(t, result)
		t.Errorf("expected IsError to be false, got error: %s", text)
	}

	text := extractText(t, result)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if parsed["metric"] != "errors" {
		t.Errorf("metric = %v, want %q", parsed["metric"], "errors")
	}
	// Verify the response has the expected structure.
	if _, ok := parsed["current"]; !ok {
		t.Error("expected 'current' key in response")
	}
	if _, ok := parsed["baseline"]; !ok {
		t.Error("expected 'baseline' key in response")
	}
	if _, ok := parsed["changes"]; !ok {
		t.Error("expected 'changes' key in response")
	}
}
