package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestServersHandler_UnknownAction(t *testing.T) {
	d := ServersDeps{
		ServerStore: mocks.NewServerStore(),
		MetricStore: mocks.NewMetricStore(),
	}

	handler := ServersHandler(d)
	req := MakeCallToolRequest("servers", map[string]any{"action": "bogus"})
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

func TestHandleListServers_Empty(t *testing.T) {
	d := ServersDeps{
		ServerStore: mocks.NewServerStore(),
		MetricStore: mocks.NewMetricStore(),
	}

	result, err := HandleListServers(context.Background(), d)
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
	if !strings.Contains(text, "No monitored servers") {
		t.Errorf("expected 'No monitored servers' message, got %q", text)
	}
}

func TestHandleQueryMetrics_MissingServerID(t *testing.T) {
	d := ServersDeps{
		ServerStore: mocks.NewServerStore(),
		MetricStore: mocks.NewMetricStore(),
	}

	args := map[string]any{}
	result, err := HandleQueryMetrics(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true for missing server_id")
	}

	text := extractText(t, result)
	if !strings.Contains(text, "server_id is required") {
		t.Errorf("expected 'server_id is required' error, got %q", text)
	}
}

func TestHandleServerHealth_MissingServerID(t *testing.T) {
	d := ServersDeps{
		ServerStore: mocks.NewServerStore(),
		MetricStore: mocks.NewMetricStore(),
	}

	args := map[string]any{}
	result, err := HandleServerHealth(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true for missing server_id")
	}

	text := extractText(t, result)
	if !strings.Contains(text, "server_id is required") {
		t.Errorf("expected 'server_id is required' error, got %q", text)
	}
}

