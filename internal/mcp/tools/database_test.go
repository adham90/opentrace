package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestDatabaseHandler_UnknownAction(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}
	handler := DatabaseHandler(deps)

	req := MakeCallToolRequest("database", map[string]any{"action": "nonexistent"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError for unknown action")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "unknown action") {
		t.Errorf("expected 'unknown action' in error text, got: %s", text)
	}
}

func TestHandleQueries_NoConnector(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}
	args := map[string]any{}

	result, err := HandleQueries(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "No database connector") {
		t.Errorf("expected 'No database connector' in error text, got: %s", text)
	}
}

func TestHandleExplain_NoConnector(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}
	// A valid SELECT query is needed to pass the guardrail check before reaching getQueryExecutor.
	args := map[string]any{"query": "SELECT 1"}

	result, err := HandleExplain(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "No database connector") {
		t.Errorf("expected 'No database connector' in error text, got: %s", text)
	}
}

func TestHandleTables_NoConnector(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}
	args := map[string]any{}

	result, err := HandleTables(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "No database connector") {
		t.Errorf("expected 'No database connector' in error text, got: %s", text)
	}
}

func TestHandleSchema_NoConnector(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}
	args := map[string]any{}

	result, err := HandleSchema(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "No database connector") {
		t.Errorf("expected 'No database connector' in error text, got: %s", text)
	}
}

func TestHandleLocks_NoConnector(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}
	args := map[string]any{}

	result, err := HandleLocks(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "No database connector") {
		t.Errorf("expected 'No database connector' in error text, got: %s", text)
	}
}

func TestHandleIndexes_NoConnector(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}
	args := map[string]any{}

	result, err := HandleIndexes(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "No database connector") {
		t.Errorf("expected 'No database connector' in error text, got: %s", text)
	}
}

func TestHandleDatabaseActivity_NoConnector(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}
	args := map[string]any{}

	result, err := HandleDatabaseActivity(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "No database connector") {
		t.Errorf("expected 'No database connector' in error text, got: %s", text)
	}
}

func TestHandleRunbookAction_UnknownRunbook(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}
	args := map[string]any{"playbook": "nonexistent_runbook"}

	result, err := HandleRunbookAction(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError for unknown runbook")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "unknown playbook") {
		t.Errorf("expected 'unknown playbook' in error text, got: %s", text)
	}
}

func TestHandleStorage_NoConnector(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}
	args := map[string]any{}

	result, err := HandleStorage(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "No database connector") {
		t.Errorf("expected 'No database connector' in error text, got: %s", text)
	}
}

func TestHandleConnections_NoConnector(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}
	args := map[string]any{}

	result, err := HandleConnections(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "No database connector") {
		t.Errorf("expected 'No database connector' in error text, got: %s", text)
	}
}

func TestRunbookErrorSpike_NoLogs(t *testing.T) {
	// RunbookErrorSpike takes a LogStore directly (no connector needed).
	// With a mock that has no entries, it should return a successful result
	// indicating no error spike was detected.
	logStore := mocks.NewLogStore()

	result, err := RunbookErrorSpike(context.Background(), logStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for empty log store")
	}

	text := extractText(t, result)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if parsed["playbook"] != "error_spike" {
		t.Errorf("playbook = %v, want %q", parsed["playbook"], "error_spike")
	}

	diagnosis, ok := parsed["diagnosis"].([]any)
	if !ok {
		t.Fatalf("expected diagnosis array, got %T", parsed["diagnosis"])
	}
	if len(diagnosis) == 0 {
		t.Fatal("expected at least one diagnosis entry")
	}
	if diagnosis[0] != "No error spike detected in last hour" {
		t.Errorf("diagnosis[0] = %v, want %q", diagnosis[0], "No error spike detected in last hour")
	}
}
