package connector

// tool_args_test.go — Comprehensive tests that simulate the full agent loop path:
// ValidateArgs (with coercion) → tool handler. Tests every tool with normal args,
// LLM-typical type mismatches, missing args, and edge cases.

import (
	"context"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/store"
)

// callTool simulates the agent loop: validate+coerce args, then call handler.
func callTool(ctx context.Context, tool agent.Tool, args map[string]any) (string, error) {
	if err := agent.ValidateArgs(tool.Params, args); err != nil {
		return "", err
	}
	return tool.Handler(ctx, args)
}

// ---------- log_search ----------

func TestLogSearch_NormalArgs(t *testing.T) {
	ms := &mockLogStore{entries: sampleLogs()}
	tools := NewLogsConnector(ms).Tools()
	result, err := callTool(context.Background(), tools[0], map[string]any{
		"query":   "timeout",
		"service": "api",
		"level":   "error",
		"limit":   float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Found 1 log entr") {
		t.Errorf("expected results, got: %s", result)
	}
}

func TestLogSearch_LimitAsString(t *testing.T) {
	ms := &mockLogStore{entries: sampleLogs()}
	tools := NewLogsConnector(ms).Tools()

	// LLM sends limit as "10" instead of 10
	result, err := callTool(context.Background(), tools[0], map[string]any{
		"query": "timeout",
		"limit": "10",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Found") {
		t.Errorf("expected results, got: %s", result)
	}
}

func TestLogSearch_LimitAsInt(t *testing.T) {
	ms := &mockLogStore{entries: sampleLogs()}
	tools := NewLogsConnector(ms).Tools()

	// Go int (not float64)
	result, err := callTool(context.Background(), tools[0], map[string]any{
		"query": "timeout",
		"limit": 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Found") {
		t.Errorf("expected results, got: %s", result)
	}
}

func TestLogSearch_AllEmpty(t *testing.T) {
	ms := &mockLogStore{entries: sampleLogs()}
	tools := NewLogsConnector(ms).Tools()

	// No args at all — all optional, should use defaults
	result, err := callTool(context.Background(), tools[0], map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestLogSearch_InvalidLimitString(t *testing.T) {
	tools := NewLogsConnector(&mockLogStore{}).Tools()

	// Non-numeric string for limit — should fail validation
	_, err := callTool(context.Background(), tools[0], map[string]any{
		"limit": "abc",
	})
	if err == nil {
		t.Fatal("expected error for non-numeric limit string")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error should mention 'limit', got: %v", err)
	}
}

func TestLogSearch_WithTimeFilters(t *testing.T) {
	ms := &mockLogStore{entries: sampleLogs()}
	tools := NewLogsConnector(ms).Tools()

	result, err := callTool(context.Background(), tools[0], map[string]any{
		"start": "2024-01-01T00:00:00Z",
		"end":   "2024-12-31T23:59:59Z",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestLogSearch_WithTraceID(t *testing.T) {
	ms := &mockLogStore{entries: sampleLogs()}
	tools := NewLogsConnector(ms).Tools()

	result, err := callTool(context.Background(), tools[0], map[string]any{
		"trace_id": "abc-123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

// ---------- echo ----------

func TestEcho_NormalArgs(t *testing.T) {
	tool := agent.EchoTool()
	result, err := callTool(context.Background(), tool, map[string]any{
		"message": "hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello world" {
		t.Errorf("expected 'hello world', got %q", result)
	}
}

func TestEcho_NumberCoercedToString(t *testing.T) {
	tool := agent.EchoTool()

	// LLM sends 42 instead of "42"
	result, err := callTool(context.Background(), tool, map[string]any{
		"message": float64(42),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "42" {
		t.Errorf("expected '42', got %q", result)
	}
}

func TestEcho_MissingRequired(t *testing.T) {
	tool := agent.EchoTool()

	_, err := callTool(context.Background(), tool, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing required message")
	}
	if !strings.Contains(err.Error(), "message") {
		t.Errorf("error should mention 'message', got: %v", err)
	}
}

func TestEcho_BoolNotCoercible(t *testing.T) {
	tool := agent.EchoTool()

	_, err := callTool(context.Background(), tool, map[string]any{
		"message": true,
	})
	if err == nil {
		t.Fatal("expected error for bool as string")
	}
}

// ---------- Comprehensive coercion matrix ----------

func TestCoercionMatrix(t *testing.T) {
	// Tests every param type with every common LLM output type
	tests := []struct {
		name      string
		paramType string
		value     any
		wantErr   bool
	}{
		// String param
		{"string_from_string", "string", "hello", false},
		{"string_from_float64", "string", float64(42), false},
		{"string_from_float_decimal", "string", float64(3.14), false},
		{"string_from_bool", "string", true, true}, // not coercible
		{"string_from_nil", "string", nil, true},

		// Int param
		{"int_from_float64", "int", float64(10), false},
		{"int_from_int", "int", 10, false},
		{"int_from_string_numeric", "int", "10", false},
		{"int_from_string_float", "int", "3.14", false},
		{"int_from_string_invalid", "int", "abc", true},
		{"int_from_bool", "int", true, true},    // not coercible
		{"int_from_nil", "int", nil, true},

		// Bool param
		{"bool_from_bool", "bool", true, false},
		{"bool_from_string_true", "bool", "true", false},
		{"bool_from_string_false", "bool", "false", false},
		{"bool_from_string_1", "bool", "1", false},
		{"bool_from_string_0", "bool", "0", false},
		{"bool_from_string_invalid", "bool", "maybe", true},
		{"bool_from_float64", "bool", float64(1), true},    // not coercible
		{"bool_from_nil", "bool", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := []agent.ToolParam{
				{Name: "val", Type: tt.paramType, Required: true},
			}
			args := map[string]any{"val": tt.value}
			err := agent.ValidateArgs(params, args)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for %T → %s", tt.value, tt.paramType)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for %T → %s: %v", tt.value, tt.paramType, err)
			}
		})
	}
}

// ---------- helpers ----------

func findTool(t *testing.T, tools []agent.Tool, name string) agent.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return agent.Tool{}
}

func sampleLogs() []store.LogEntry {
	return []store.LogEntry{
		{
			Level:   "ERROR",
			Service: "api",
			Message: "connection timeout",
		},
	}
}

