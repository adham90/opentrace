package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

func TestErrorImpactHandler_Success(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	egs := store.NewErrorGroupStore(db)
	eis := store.NewErrorImpactStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entry := store.LogEntry{
		Timestamp:        now.Add(-5 * time.Minute),
		Level:            "ERROR",
		Service:          "api",
		Message:          "connection refused",
		ExceptionClass:   "ConnectionError",
		ErrorFingerprint: "fp-impact-1",
	}
	ls.BatchInsert(ctx, []store.LogEntry{entry})
	egs.Upsert(ctx, entry)

	// Track 3 users
	eis.TrackImpact(ctx, "fp-impact-1", "user-a", map[string]any{"browser": "Chrome"}, 1, "api")
	eis.TrackImpact(ctx, "fp-impact-1", "user-b", map[string]any{"browser": "Safari"}, 2, "api")
	eis.TrackImpact(ctx, "fp-impact-1", "user-c", map[string]any{"browser": "Chrome"}, 3, "api")

	// Compute scores to populate impact data
	eis.ComputeImpactScores(ctx)

	handler := errorImpactHandler(eis, egs)
	result, err := handler(ctx, makeRequest(map[string]any{"fingerprint": "fp-impact-1"}))
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

	if resp["unique_users"] != float64(3) {
		t.Errorf("unique_users = %v, want 3", resp["unique_users"])
	}
	if resp["total_occurrences"] != float64(3) {
		t.Errorf("total_occurrences = %v, want 3", resp["total_occurrences"])
	}
	if resp["exception_class"] != "ConnectionError" {
		t.Errorf("exception_class = %v, want ConnectionError", resp["exception_class"])
	}

	users, ok := resp["affected_users"].([]any)
	if !ok {
		t.Fatal("expected affected_users array")
	}
	if len(users) != 3 {
		t.Errorf("len(affected_users) = %d, want 3", len(users))
	}
}

func TestErrorImpactHandler_MissingFingerprint(t *testing.T) {
	db := setupTestDB(t)
	eis := store.NewErrorImpactStore(db)

	handler := errorImpactHandler(eis, nil)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when fingerprint missing")
	}
}

func TestErrorImpactHandler_NilStore(t *testing.T) {
	handler := errorImpactHandler(nil, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{"fingerprint": "fp1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when store is nil")
	}
}

func TestUserErrorsHandler_Success(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	egs := store.NewErrorGroupStore(db)
	eis := store.NewErrorImpactStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Create two error groups
	for i, fp := range []string{"fp-ue-1", "fp-ue-2"} {
		entry := store.LogEntry{
			Timestamp:        now.Add(-time.Duration(i+1) * time.Minute),
			Level:            "ERROR",
			Service:          "api",
			Message:          fmt.Sprintf("error %d", i+1),
			ExceptionClass:   fmt.Sprintf("Error%d", i+1),
			ErrorFingerprint: fp,
		}
		ls.BatchInsert(ctx, []store.LogEntry{entry})
		egs.Upsert(ctx, entry)
		eis.TrackImpact(ctx, fp, "user-42", nil, int64(i+1), "api")
	}

	handler := userErrorsHandler(eis)
	result, err := handler(ctx, makeRequest(map[string]any{
		"user_id": "user-42",
		"since":   "1h",
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

	if resp["user_id"] != "user-42" {
		t.Errorf("user_id = %v, want user-42", resp["user_id"])
	}
	if resp["error_count"] != float64(2) {
		t.Errorf("error_count = %v, want 2", resp["error_count"])
	}
}

func TestUserErrorsHandler_Empty(t *testing.T) {
	db := setupTestDB(t)
	eis := store.NewErrorImpactStore(db)

	handler := userErrorsHandler(eis)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"user_id": "nonexistent",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "No errors found") {
		t.Errorf("expected empty message, got: %s", text)
	}
}

func TestUserErrorsHandler_MissingUserID(t *testing.T) {
	db := setupTestDB(t)
	eis := store.NewErrorImpactStore(db)

	handler := userErrorsHandler(eis)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when user_id missing")
	}
}

func TestTopErrorsByImpactHandler_Success(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	egs := store.NewErrorGroupStore(db)
	eis := store.NewErrorImpactStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Error A: 3 users (high impact)
	entryA := store.LogEntry{
		Timestamp:        now.Add(-5 * time.Minute),
		Level:            "ERROR",
		Service:          "api",
		Message:          "error A",
		ExceptionClass:   "ErrorA",
		ErrorFingerprint: "fp-top-a",
	}
	ls.BatchInsert(ctx, []store.LogEntry{entryA})
	egs.Upsert(ctx, entryA)
	eis.TrackImpact(ctx, "fp-top-a", "u1", nil, 1, "api")
	eis.TrackImpact(ctx, "fp-top-a", "u2", nil, 2, "api")
	eis.TrackImpact(ctx, "fp-top-a", "u3", nil, 3, "api")

	// Error B: 1 user (low impact)
	entryB := store.LogEntry{
		Timestamp:        now.Add(-5 * time.Minute),
		Level:            "ERROR",
		Service:          "api",
		Message:          "error B",
		ExceptionClass:   "ErrorB",
		ErrorFingerprint: "fp-top-b",
	}
	ls.BatchInsert(ctx, []store.LogEntry{entryB})
	egs.Upsert(ctx, entryB)
	eis.TrackImpact(ctx, "fp-top-b", "u1", nil, 4, "api")

	// Compute scores
	eis.ComputeImpactScores(ctx)

	handler := topErrorsByImpactHandler(eis)
	result, err := handler(ctx, makeRequest(map[string]any{
		"since": "3h",
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

	if resp["result_count"] != float64(2) {
		t.Errorf("result_count = %v, want 2", resp["result_count"])
	}

	errors, ok := resp["errors"].([]any)
	if !ok {
		t.Fatal("expected errors array")
	}
	if len(errors) < 2 {
		t.Fatalf("got %d errors, want >= 2", len(errors))
	}

	// Error A should rank first (more unique users = higher impact)
	first := errors[0].(map[string]any)
	if first["fingerprint"] != "fp-top-a" {
		t.Errorf("top error = %v, want fp-top-a", first["fingerprint"])
	}
	if first["unique_users"] != float64(3) {
		t.Errorf("unique_users = %v, want 3", first["unique_users"])
	}
}

func TestTopErrorsByImpactHandler_Empty(t *testing.T) {
	db := setupTestDB(t)
	eis := store.NewErrorImpactStore(db)

	handler := topErrorsByImpactHandler(eis)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"since": "1h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "No errors found") {
		t.Errorf("expected empty message, got: %s", text)
	}
}
