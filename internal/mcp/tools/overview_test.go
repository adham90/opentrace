package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestOverviewHandler_UnknownAction(t *testing.T) {
	d := OverviewDeps{
		LogStore:         mocks.NewLogStore(),
		DSStore:          mocks.NewDataSourceStore(),
		ServerStore:      mocks.NewServerStore(),
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		WatchStore:       mocks.NewWatchStore(),
		HealthCheckStore: mocks.NewHealthCheckStore(),
		SettingsStore:    mocks.NewSettingsStore(),
		AgentNoteStore:   mocks.NewAgentNoteStore(),
	}

	handler := OverviewHandler(d)
	req := MakeCallToolRequest("overview", map[string]any{"action": "bogus"})
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

func TestHandleOverviewStatus_EmptyDB(t *testing.T) {
	d := OverviewDeps{
		LogStore:         mocks.NewLogStore(),
		DSStore:          mocks.NewDataSourceStore(),
		ServerStore:      mocks.NewServerStore(),
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		WatchStore:       mocks.NewWatchStore(),
		HealthCheckStore: mocks.NewHealthCheckStore(),
		SettingsStore:    mocks.NewSettingsStore(),
	}

	result, err := HandleOverviewStatus(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	// The result should be valid JSON.
	text := extractText(t, result)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}

	// With empty stores, connectors and servers should show zeros.
	if connectors, ok := data["connectors"].(map[string]any); ok {
		if total, _ := connectors["total"].(float64); total != 0 {
			t.Errorf("expected 0 total connectors, got %v", total)
		}
	}
	if servers, ok := data["servers"].(map[string]any); ok {
		if total, _ := servers["total"].(float64); total != 0 {
			t.Errorf("expected 0 total servers, got %v", total)
		}
	}
}

func TestHandleTriage_EmptyDB(t *testing.T) {
	d := OverviewDeps{
		LogStore:         mocks.NewLogStore(),
		DSStore:          mocks.NewDataSourceStore(),
		ServerStore:      mocks.NewServerStore(),
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		WatchStore:       mocks.NewWatchStore(),
		HealthCheckStore: mocks.NewHealthCheckStore(),
	}

	result, err := HandleTriage(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	// Empty DB should yield the "healthy" message.
	text := extractText(t, result)
	if !strings.Contains(text, "healthy") {
		t.Errorf("expected healthy message, got %q", text)
	}
}

func TestHandleTimeline_EmptyDB(t *testing.T) {
	d := OverviewDeps{
		LogStore:         mocks.NewLogStore(),
		DSStore:          mocks.NewDataSourceStore(),
		ServerStore:      mocks.NewServerStore(),
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		WatchStore:       mocks.NewWatchStore(),
		HealthCheckStore: mocks.NewHealthCheckStore(),
	}

	args := map[string]any{
		"start": "2025-01-01T00:00:00Z",
		"end":   "2025-01-01T01:00:00Z",
	}
	result, err := HandleTimeline(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	// Should be valid JSON with an events array.
	text := extractText(t, result)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
}

func TestHandleOverviewSettings(t *testing.T) {
	d := OverviewDeps{
		SettingsStore: mocks.NewSettingsStore(),
	}

	result, err := HandleOverviewSettings(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	// Should be valid JSON containing retention_days (default 30 from mock).
	text := extractText(t, result)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
	if days, ok := data["retention_days"].(float64); !ok || days != 30 {
		t.Errorf("expected retention_days=30, got %v", data["retention_days"])
	}
}

func TestHandleOverviewNotes_Create(t *testing.T) {
	d := OverviewDeps{
		AgentNoteStore: mocks.NewAgentNoteStore(),
	}

	args := map[string]any{
		"entity_type": "service",
		"entity_id":   "api-server",
		"note":        "This service handles user auth",
	}
	result, err := HandleOverviewNotes(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	text := extractText(t, result)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
	if msg, _ := data["message"].(string); !strings.Contains(msg, "Note saved") {
		t.Errorf("expected 'Note saved' message, got %q", msg)
	}
}

func TestHandleOverviewNotes_List(t *testing.T) {
	d := OverviewDeps{
		AgentNoteStore: mocks.NewAgentNoteStore(),
	}

	// List with no notes should return the "no notes" message.
	args := map[string]any{}
	result, err := HandleOverviewNotes(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	text := extractText(t, result)
	if !strings.Contains(text, "No agent notes") {
		t.Errorf("expected 'No agent notes' message, got %q", text)
	}
}

func TestHandleDiagnose_EmptyDB(t *testing.T) {
	d := OverviewDeps{
		LogStore:         mocks.NewLogStore(),
		DSStore:          mocks.NewDataSourceStore(),
		ServerStore:      mocks.NewServerStore(),
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		WatchStore:       mocks.NewWatchStore(),
		HealthCheckStore: mocks.NewHealthCheckStore(),
	}

	args := map[string]any{}
	result, err := HandleDiagnose(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	text := extractText(t, result)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
	// Should contain timeframe and since/until fields.
	if _, ok := data["timeframe"]; !ok {
		t.Error("expected 'timeframe' field in response")
	}
	if _, ok := data["since"]; !ok {
		t.Error("expected 'since' field in response")
	}
}

func TestHandleDiagnose_WithService(t *testing.T) {
	d := OverviewDeps{
		LogStore:         mocks.NewLogStore(),
		DSStore:          mocks.NewDataSourceStore(),
		ServerStore:      mocks.NewServerStore(),
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		WatchStore:       mocks.NewWatchStore(),
		HealthCheckStore: mocks.NewHealthCheckStore(),
	}

	args := map[string]any{"service": "web"}
	result, err := HandleDiagnose(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	text := extractText(t, result)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
	if svc, _ := data["service"].(string); svc != "web" {
		t.Errorf("expected service='web', got %q", svc)
	}
}

func TestHandleOverviewInvestigate_MissingTarget(t *testing.T) {
	d := OverviewDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
		WatchStore:      mocks.NewWatchStore(),
	}

	// No service provided — should return an error.
	args := map[string]any{}
	result, err := HandleOverviewInvestigate(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when service is missing")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "service is required") {
		t.Errorf("expected 'service is required' message, got %q", text)
	}
}

func TestHandleChanges_EmptyDB(t *testing.T) {
	d := OverviewDeps{
		LogStore:         mocks.NewLogStore(),
		DSStore:          mocks.NewDataSourceStore(),
		ServerStore:      mocks.NewServerStore(),
		ErrorGroupStore:  mocks.NewErrorGroupStore(),
		WatchStore:       mocks.NewWatchStore(),
		HealthCheckStore: mocks.NewHealthCheckStore(),
	}

	args := map[string]any{}
	result, err := HandleChanges(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	text := extractText(t, result)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
	if _, ok := data["since"]; !ok {
		t.Error("expected 'since' field in response")
	}
}

func TestHandleOverviewDeleteNote_MissingFields(t *testing.T) {
	d := OverviewDeps{
		AgentNoteStore: mocks.NewAgentNoteStore(),
	}

	// Missing entity_type should return an error.
	args := map[string]any{"entity_id": "some-id"}
	result, err := HandleOverviewDeleteNote(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true when entity_type is missing")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "entity_type is required") {
		t.Errorf("expected 'entity_type is required' message, got %q", text)
	}
}
