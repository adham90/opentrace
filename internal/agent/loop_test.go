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
	responses []string
	callIndex int
}

func (m *mockLLM) ChatCompletion(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	if m.callIndex >= len(m.responses) {
		return llm.ChatResponse{}, fmt.Errorf("mock: no more responses")
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return llm.ChatResponse{Content: resp}, nil
}

func defaultCfg() RunConfig {
	return RunConfig{
		MaxSteps:            12,
		MaxToolCalls:        8,
		MaxObservationBytes: 8192,
	}
}

func TestRun_FinalAnswerImmediate(t *testing.T) {
	mock := &mockLLM{responses: []string{
		`{"type":"final_answer","content":"The answer is 42."}`,
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
	mock := &mockLLM{responses: []string{
		`{"type":"tool_call","tool":"echo","args":{"message":"hello"}}`,
		`{"type":"final_answer","content":"Echo said hello."}`,
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
	mock := &mockLLM{responses: []string{
		`{"type":"tool_call","tool":"echo","args":{"message":"test message"}}`,
		`{"type":"final_answer","content":"done"}`,
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
	// LLM always returns a tool call — should exceed max steps
	responses := make([]string, 15)
	for i := range responses {
		responses[i] = `{"type":"tool_call","tool":"echo","args":{"message":"again"}}`
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
	responses := make([]string, 10)
	for i := range responses {
		responses[i] = `{"type":"tool_call","tool":"echo","args":{"message":"x"}}`
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

func TestRun_MalformedJSON_Retry(t *testing.T) {
	mock := &mockLLM{responses: []string{
		`Sure! Here is some text {"type":"final_answer","content":"repaired"} and trailing`,
		// ^ malformed, but ParseResponse will repair it via brace extraction
		// Actually this will be repaired in ParseResponse itself.
		// Let's use truly malformed that fails even repair:
	}}
	// Actually, the above will be repaired by ParseResponse. Let's test
	// the retry path: first response is total garbage, second is valid.
	mock = &mockLLM{responses: []string{
		`this is garbage with no json`,
		`{"type":"final_answer","content":"recovered"}`,
	}}
	a := New(mock, defaultCfg())

	result, err := a.Run(context.Background(), "test retry", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "recovered" {
		t.Errorf("expected 'recovered', got %q", result)
	}
}

func TestRun_ObservationTruncated(t *testing.T) {
	// Tool returns a huge output
	hugeMsg := strings.Repeat("x", 200)
	mock := &mockLLM{responses: []string{
		fmt.Sprintf(`{"type":"tool_call","tool":"echo","args":{"message":"%s"}}`, hugeMsg),
		`{"type":"final_answer","content":"done"}`,
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

func TestRun_InvalidToolArgs(t *testing.T) {
	// Call echo with wrong type for 'message'
	mock := &mockLLM{responses: []string{
		`{"type":"tool_call","tool":"echo","args":{"message":42}}`,
		`{"type":"final_answer","content":"fixed"}`,
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
	// Verify the mock was called twice (invalid args → retry → final answer)
	if mock.callIndex != 2 {
		t.Errorf("expected 2 LLM calls, got %d", mock.callIndex)
	}
}

func TestRun_UnknownTool(t *testing.T) {
	mock := &mockLLM{responses: []string{
		`{"type":"tool_call","tool":"nonexistent","args":{}}`,
		`{"type":"final_answer","content":"ok"}`,
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
	if mock.callIndex != 2 {
		t.Errorf("expected 2 LLM calls (unknown tool + final), got %d", mock.callIndex)
	}
}

func TestRun_ContextCancelled(t *testing.T) {
	mock := &mockLLM{responses: []string{
		`{"type":"final_answer","content":"should not reach"}`,
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
