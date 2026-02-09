package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/opentrace/opentrace/internal/llm"
)

// mockLLM returns pre-programmed responses in order.
type mockLLM struct {
	responses []llm.ChatResponse
	callIndex int
}

func (m *mockLLM) ChatCompletion(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	if m.callIndex >= len(m.responses) {
		return llm.ChatResponse{}, fmt.Errorf("mock: no more responses")
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

func textResp(content string) llm.ChatResponse {
	return llm.ChatResponse{Content: content}
}

func toolResp(name string, args map[string]any) llm.ChatResponse {
	return llm.ChatResponse{
		ToolCalls: []llm.ToolCall{{
			ID:   "call_" + name,
			Name: name,
			Args: args,
		}},
	}
}

func defaultCfg() RunConfig {
	return RunConfig{
		MaxSteps:            12,
		MaxToolCalls:        8,
		MaxObservationBytes: 8192,
	}
}

func TestRun_FinalAnswerImmediate(t *testing.T) {
	mock := &mockLLM{responses: []llm.ChatResponse{
		textResp("The answer is 42."),
	}}
	a := New(mock, defaultCfg())

	result, err := a.Run(context.Background(), "What is the answer?", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "The answer is 42." {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestRun_ToolCallThenFinal(t *testing.T) {
	mock := &mockLLM{responses: []llm.ChatResponse{
		toolResp("echo", map[string]any{"message": "hello"}),
		textResp("Echo said hello."),
	}}
	a := New(mock, defaultCfg())
	tools := []Tool{EchoTool()}

	result, err := a.Run(context.Background(), "echo hello", tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Echo said hello." {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestRun_EchoTool(t *testing.T) {
	mock := &mockLLM{responses: []llm.ChatResponse{
		toolResp("echo", map[string]any{"message": "test message"}),
		textResp("done"),
	}}
	a := New(mock, defaultCfg())
	tools := []Tool{EchoTool()}

	var events []Event
	cb := func(e Event) { events = append(events, e) }

	result, err := a.RunWithCallback(context.Background(), "echo test", tools, cb, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Errorf("unexpected result: %q", result)
	}

	// Verify we got an observation event with the echoed message
	found := false
	for _, e := range events {
		if e.Type == "observation" && e.Content == "test message" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected observation event with 'test message'")
	}
}

func TestRun_MaxStepsExceeded(t *testing.T) {
	responses := make([]llm.ChatResponse, 15)
	for i := range responses {
		responses[i] = toolResp("echo", map[string]any{"message": "again"})
	}
	mock := &mockLLM{responses: responses}
	cfg := RunConfig{MaxSteps: 3, MaxToolCalls: 100, MaxObservationBytes: 8192}
	a := New(mock, cfg)
	tools := []Tool{EchoTool()}

	_, err := a.Run(context.Background(), "loop forever", tools)
	if err == nil {
		t.Fatal("expected error for max steps exceeded")
	}
	if !strings.Contains(err.Error(), "max steps") {
		t.Errorf("expected 'max steps' in error, got: %v", err)
	}
}

func TestRun_ToolBudgetExhausted(t *testing.T) {
	responses := make([]llm.ChatResponse, 10)
	for i := range responses {
		responses[i] = toolResp("echo", map[string]any{"message": "x"})
	}
	mock := &mockLLM{responses: responses}
	cfg := RunConfig{MaxSteps: 20, MaxToolCalls: 2, MaxObservationBytes: 8192}
	a := New(mock, cfg)
	tools := []Tool{EchoTool()}

	_, err := a.Run(context.Background(), "use tools", tools)
	if err == nil {
		t.Fatal("expected error for tool budget exhausted")
	}
	if !strings.Contains(err.Error(), "tool call budget") {
		t.Errorf("expected 'tool call budget' in error, got: %v", err)
	}
}

func TestRun_ObservationTruncated(t *testing.T) {
	hugeMsg := strings.Repeat("x", 200)
	mock := &mockLLM{responses: []llm.ChatResponse{
		toolResp("echo", map[string]any{"message": hugeMsg}),
		textResp("done"),
	}}
	cfg := RunConfig{MaxSteps: 12, MaxToolCalls: 8, MaxObservationBytes: 50}
	a := New(mock, cfg)
	tools := []Tool{EchoTool()}

	var events []Event
	cb := func(e Event) { events = append(events, e) }

	_, err := a.RunWithCallback(context.Background(), "test truncate", tools, cb, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range events {
		if e.Type == "observation" {
			if !strings.HasSuffix(e.Content, "\n[truncated]") {
				t.Error("expected observation to be truncated")
			}
			return
		}
	}
	t.Error("no observation event found")
}

func TestRun_CoercedToolArgs(t *testing.T) {
	mock := &mockLLM{responses: []llm.ChatResponse{
		// echo with number instead of string — coerced to "42"
		toolResp("echo", map[string]any{"message": float64(42)}),
		textResp("done"),
	}}
	a := New(mock, defaultCfg())
	tools := []Tool{EchoTool()}

	var events []Event
	cb := func(e Event) { events = append(events, e) }

	result, err := a.RunWithCallback(context.Background(), "coerce args", tools, cb, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Errorf("expected 'done', got %q", result)
	}
	// Verify the coerced value was echoed back
	for _, e := range events {
		if e.Type == "observation" && e.Content == "42" {
			return
		}
	}
	t.Error("expected observation event with coerced value '42'")
}

func TestRun_InvalidToolArgs(t *testing.T) {
	mock := &mockLLM{responses: []llm.ChatResponse{
		// echo with bool instead of string — not coercible
		toolResp("echo", map[string]any{"message": true}),
		textResp("fixed"),
	}}
	a := New(mock, defaultCfg())
	tools := []Tool{EchoTool()}

	result, err := a.Run(context.Background(), "bad args", tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "fixed" {
		t.Errorf("expected 'fixed', got %q", result)
	}
}

func TestRun_UnknownTool(t *testing.T) {
	mock := &mockLLM{responses: []llm.ChatResponse{
		toolResp("nonexistent", map[string]any{}),
		textResp("ok"),
	}}
	a := New(mock, defaultCfg())
	tools := []Tool{EchoTool()}

	result, err := a.Run(context.Background(), "unknown tool", tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}

func TestRun_ContextCancelled(t *testing.T) {
	mock := &mockLLM{responses: []llm.ChatResponse{
		textResp("should not reach"),
	}}
	a := New(mock, defaultCfg())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := a.Run(ctx, "cancelled", nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Errorf("expected 'canceled' in error, got: %v", err)
	}
}

func TestRun_MultiToolCallThenFinal(t *testing.T) {
	mock := &mockLLM{responses: []llm.ChatResponse{
		toolResp("echo", map[string]any{"message": "step1"}),
		toolResp("echo", map[string]any{"message": "step2"}),
		textResp("All done."),
	}}
	a := New(mock, defaultCfg())
	tools := []Tool{EchoTool()}

	var events []Event
	cb := func(e Event) { events = append(events, e) }

	result, err := a.RunWithCallback(context.Background(), "multi step", tools, cb, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "All done." {
		t.Errorf("unexpected result: %q", result)
	}

	// Verify event sequence: tool_call, observation, tool_call, observation, thinking, final
	expectedTypes := []string{"tool_call", "observation", "tool_call", "observation", "thinking", "final"}
	if len(events) != len(expectedTypes) {
		t.Fatalf("expected %d events, got %d: %v", len(expectedTypes), len(events), eventTypes(events))
	}
	for i, et := range expectedTypes {
		if events[i].Type != et {
			t.Errorf("event[%d]: expected %q, got %q", i, et, events[i].Type)
		}
	}
}

func TestRun_ToolCallEmitsArgs(t *testing.T) {
	mock := &mockLLM{responses: []llm.ChatResponse{
		toolResp("echo", map[string]any{"message": "hello"}),
		textResp("done"),
	}}
	a := New(mock, defaultCfg())
	tools := []Tool{EchoTool()}

	var events []Event
	cb := func(e Event) { events = append(events, e) }

	_, err := a.RunWithCallback(context.Background(), "test args", tools, cb, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range events {
		if e.Type == "tool_call" {
			if e.ToolName != "echo" {
				t.Errorf("expected tool name 'echo', got %q", e.ToolName)
			}
			if msg, ok := e.Args["message"]; !ok || msg != "hello" {
				t.Errorf("expected args with message=hello, got %v", e.Args)
			}
			return
		}
	}
	t.Error("no tool_call event found")
}

func eventTypes(events []Event) []string {
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}
