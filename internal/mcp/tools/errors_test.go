package tools

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

func TestErrorsHandler_UnknownAction(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}
	handler := ErrorsHandler(deps)

	req := MakeCallToolRequest("errors", map[string]any{"action": "nonexistent"})
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

func TestErrorsHandler_MissingAction(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}
	handler := ErrorsHandler(deps)

	req := MakeCallToolRequest("errors", map[string]any{})
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

func TestErrorsList_Empty(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	args := map[string]any{"action": "list"}
	result, err := ErrorsList(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for empty list")
	}
	// Empty list returns a text message, not JSON with error_groups.
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestErrorsList_WithGroups(t *testing.T) {
	egs := mocks.NewErrorGroupStore()
	now := time.Now().UTC()
	egs.Groups["fp-abc"] = &store.ErrorGroup{
		Fingerprint:     "fp-abc",
		Service:         "api",
		ExceptionClass:  "RuntimeError",
		Message:         "nil pointer dereference",
		Status:          store.ErrorGroupUnresolved,
		OccurrenceCount: 42,
		FirstSeenAt:     now.Add(-time.Hour),
		LastSeenAt:      now,
	}

	deps := ErrorsDeps{
		ErrorGroupStore:  egs,
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	args := map[string]any{"action": "list"}
	result, err := ErrorsList(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}
}

func TestErrorsDetail_MissingFingerprint(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	args := map[string]any{"action": "detail"}
	result, err := ErrorsDetail(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when fingerprint is missing")
	}
}

func TestErrorsDetail_NotFound(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	args := map[string]any{"action": "detail", "fingerprint": "nonexistent"}
	result, err := ErrorsDetail(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when fingerprint not found")
	}
}

func TestErrorsResolve_MissingFingerprint(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	args := map[string]any{"action": "resolve"}
	result, err := ErrorsResolve(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when fingerprint is missing")
	}
}

func TestErrorsResolve_MissingReason(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	args := map[string]any{"action": "resolve", "fingerprint": "fp-abc"}
	result, err := ErrorsResolve(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when reason is missing")
	}
}

func TestErrorsIgnore_MissingFingerprint(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	args := map[string]any{"action": "ignore"}
	result, err := ErrorsIgnore(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when fingerprint is missing")
	}
}

func TestErrorsIgnore_MissingReason(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	args := map[string]any{"action": "ignore", "fingerprint": "fp-abc"}
	result, err := ErrorsIgnore(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when reason is missing")
	}
}

func TestErrorsInvestigate_MissingAnchor(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	// Neither log_id nor trace_id provided.
	args := map[string]any{"action": "investigate"}
	result, err := ErrorsInvestigate(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when log_id and trace_id are missing")
	}
}

func TestErrorsImpact_MissingFingerprint(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	args := map[string]any{"action": "impact"}
	result, err := ErrorsImpact(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when fingerprint is missing")
	}
}

func TestErrorsUserErrors_MissingUserID(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	args := map[string]any{"action": "user_errors"}
	result, err := ErrorsUserErrors(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when user_id is missing")
	}
}

func TestErrorsRanking_Empty(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	args := map[string]any{"action": "ranking"}
	result, err := ErrorsRanking(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for empty ranking")
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestHandleNewErrors_Empty(t *testing.T) {
	deps := ErrorsDeps{
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		LogStore:         mocks.NewLogStore(),
		ErrorImpactStore: mocks.NewErrorImpactStore(),
	}

	args := map[string]any{"action": "new"}
	result, err := HandleNewErrors(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for empty new errors")
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}
