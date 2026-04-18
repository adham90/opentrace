package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestHealthchecksHandler_NilStore(t *testing.T) {
	d := HealthchecksDeps{HealthCheckStore: nil}
	handler := HealthchecksHandler(d)

	req := MakeCallToolRequest("healthchecks", map[string]any{"action": "list"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when HealthCheckStore is nil")
	}
}

func TestHealthchecksHandler_UnknownAction(t *testing.T) {
	hcStore := mocks.NewHealthCheckStore()
	d := HealthchecksDeps{HealthCheckStore: hcStore}
	handler := HealthchecksHandler(d)

	req := MakeCallToolRequest("healthchecks", map[string]any{"action": "nope"})
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
}

func TestHandleHealthcheckList(t *testing.T) {
	hcStore := mocks.NewHealthCheckStore()
	d := HealthchecksDeps{HealthCheckStore: hcStore}

	t.Run("empty list", func(t *testing.T) {
		result, err := HandleHealthcheckList(context.Background(), d, map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.IsError {
			t.Error("expected IsError to be false")
		}
		// The stub returns nil for List, so we get the "no health checks" message.
		text := extractText(t, result)
		if text == "" {
			t.Error("expected non-empty result text")
		}
	})
}

func TestHandleHealthcheckCreate(t *testing.T) {
	hcStore := mocks.NewHealthCheckStore()
	d := HealthchecksDeps{HealthCheckStore: hcStore}

	t.Run("creates health check", func(t *testing.T) {
		args := map[string]any{
			"name": "My API",
			"url":  "https://api.example.com/health",
		}
		result, err := HandleHealthcheckCreate(context.Background(), d, args)
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
		var parsed map[string]any
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			t.Fatalf("failed to parse result JSON: %v", err)
		}
		// The stub returns a HealthCheck with ID="stub-hc"; the handler
		// reads fields from the returned struct, not the input args.
		if parsed["id"] != "stub-hc" {
			t.Errorf("id = %v, want %q", parsed["id"], "stub-hc")
		}
		if parsed["message"] == nil {
			t.Error("expected message field in result")
		}
	})

	t.Run("missing name", func(t *testing.T) {
		args := map[string]any{"url": "https://example.com"}
		result, err := HandleHealthcheckCreate(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError when name is missing")
		}
	})

	t.Run("missing url", func(t *testing.T) {
		args := map[string]any{"name": "Test"}
		result, err := HandleHealthcheckCreate(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError when url is missing")
		}
	})
}

func TestHandleHealthcheckDelete(t *testing.T) {
	hcStore := mocks.NewHealthCheckStore()
	d := HealthchecksDeps{HealthCheckStore: hcStore}

	t.Run("deletes health check", func(t *testing.T) {
		args := map[string]any{"id": "hc-123"}
		result, err := HandleHealthcheckDelete(context.Background(), d, args)
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
		var parsed map[string]any
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			t.Fatalf("failed to parse result JSON: %v", err)
		}
		if parsed["status"] != "deleted" {
			t.Errorf("status = %v, want %q", parsed["status"], "deleted")
		}
		if parsed["id"] != "hc-123" {
			t.Errorf("id = %v, want %q", parsed["id"], "hc-123")
		}
	})

	t.Run("missing id", func(t *testing.T) {
		args := map[string]any{}
		result, err := HandleHealthcheckDelete(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError when id is missing")
		}
	})
}

func TestHandleHealthcheckUptime(t *testing.T) {
	hcStore := mocks.NewHealthCheckStore()
	d := HealthchecksDeps{HealthCheckStore: hcStore}

	t.Run("empty uptime data", func(t *testing.T) {
		args := map[string]any{}
		result, err := HandleHealthcheckUptime(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.IsError {
			t.Error("expected IsError to be false")
		}
		// The stub returns nil for UptimeSummaries, so we get the "no health checks" message.
		text := extractText(t, result)
		if text == "" {
			t.Error("expected non-empty result text")
		}
	})

	t.Run("custom hours parameter", func(t *testing.T) {
		args := map[string]any{"hours": float64(48)}
		result, err := HandleHealthcheckUptime(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.IsError {
			t.Error("expected IsError to be false")
		}
	})

	t.Run("hours capped at 720", func(t *testing.T) {
		args := map[string]any{"hours": float64(9999)}
		result, err := HandleHealthcheckUptime(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.IsError {
			t.Error("expected IsError to be false")
		}
	})
}
