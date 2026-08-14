package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestConnectorsHandler_UnknownAction(t *testing.T) {
	d := ConnectorsDeps{
		DSStore: mocks.NewDataSourceStore(),
	}

	handler := ConnectorsHandler(d)
	req := MakeCallToolRequest("connectors", map[string]any{"action": "bogus"})
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

func TestHandleConnectorList_Empty(t *testing.T) {
	d := ConnectorsDeps{
		DSStore: mocks.NewDataSourceStore(),
	}

	args := map[string]any{}
	result, err := HandleConnectorList(context.Background(), d, args)
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
	if !strings.Contains(text, "No connectors found") {
		t.Errorf("expected 'No connectors found' message, got %q", text)
	}
}

func TestHandleConnectorGet_MissingID(t *testing.T) {
	d := ConnectorsDeps{
		DSStore: mocks.NewDataSourceStore(),
	}

	// No connector_id provided.
	args := map[string]any{}
	result, err := HandleConnectorGet(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true for missing connector_id")
	}

	text := extractText(t, result)
	if !strings.Contains(text, "connector_id is required") {
		t.Errorf("expected 'connector_id is required' error, got %q", text)
	}
}

func TestHandleConnectorCreate_MissingType(t *testing.T) {
	d := ConnectorsDeps{
		IsAdmin: true, // write action: admin-gated
		DSStore: mocks.NewDataSourceStore(),
	}

	// Provide name but omit connector_type.
	args := map[string]any{
		"name": "my-database",
	}
	result, err := HandleConnectorCreate(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true for missing connector_type")
	}

	text := extractText(t, result)
	if !strings.Contains(text, "connector_type is required") {
		t.Errorf("expected 'connector_type is required' error, got %q", text)
	}
}

func TestHandleConnectorDelete_MissingID(t *testing.T) {
	d := ConnectorsDeps{
		IsAdmin: true, // write action: admin-gated
		DSStore: mocks.NewDataSourceStore(),
	}

	// No connector_id provided.
	args := map[string]any{}
	result, err := HandleConnectorDelete(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true for missing connector_id")
	}

	text := extractText(t, result)
	if !strings.Contains(text, "connector_id is required") {
		t.Errorf("expected 'connector_id is required' error, got %q", text)
	}
}
