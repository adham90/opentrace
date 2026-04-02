package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
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

