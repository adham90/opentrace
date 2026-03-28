package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestWatchesHandler_UnknownAction(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}
	handler := WatchesHandler(deps)

	req := MakeCallToolRequest("watches", map[string]any{"action": "nonexistent"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true for unknown action")
	}
}

func TestWatchesHandler_MissingAction(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}
	handler := WatchesHandler(deps)

	req := MakeCallToolRequest("watches", map[string]any{})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when action is missing")
	}
}

func TestHandleWatchStatus(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}

	args := map[string]any{"action": "status"}
	result, err := HandleWatchStatus(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for status action")
	}

	// Verify JSON structure.
	text := extractText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if _, ok := resp["watches"]; !ok {
		t.Error("expected 'watches' field in response")
	}
	if _, ok := resp["total"]; !ok {
		t.Error("expected 'total' field in response")
	}
	if _, ok := resp["pending_alerts"]; !ok {
		t.Error("expected 'pending_alerts' field in response")
	}
}

func TestHandleWatchCreate_MissingFields(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}

	// Create with no metric — the store stub still succeeds, but we verify
	// the handler itself runs without error. The validation of metric/operator
	// happens at the store level.
	args := map[string]any{"action": "create"}
	result, err := HandleWatchCreate(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// The stub returns a watch even with empty params, so this should succeed.
	// The key assertion: no panic, no Go-level error.
}

func TestHandleWatchCreate_WithParams(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}

	args := map[string]any{
		"action":    "create",
		"metric":    "error_rate",
		"operator":  "gt",
		"threshold": float64(5.0),
		"service":   "api",
		"duration":  "1h",
		"urgency":   "high",
	}
	result, err := HandleWatchCreate(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for valid create")
	}
}

func TestHandleWatchDelete_MissingWatchID(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}

	args := map[string]any{"action": "delete"}
	result, err := HandleWatchDelete(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when watch_id is missing")
	}
}

func TestHandleWatchAlerts(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}

	args := map[string]any{"action": "alerts"}
	result, err := HandleWatchAlerts(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for alerts action")
	}

	// Verify JSON structure.
	text := extractText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if _, ok := resp["count"]; !ok {
		t.Error("expected 'count' field in response")
	}
}

func TestHandleWatchDismiss_MissingAlertID(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}

	args := map[string]any{"action": "dismiss"}
	result, err := HandleWatchDismiss(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when alert_id is missing")
	}
}

func TestHandleWatchAcknowledge_MissingAlertID(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}

	args := map[string]any{"action": "acknowledge"}
	result, err := HandleWatchAcknowledge(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when alert_id is missing")
	}
}
