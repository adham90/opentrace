package agent

import (
	"testing"
)

func TestParseResponse_FinalAnswer(t *testing.T) {
	raw := `{"type":"final_answer","content":"The error is in line 42."}`
	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Type != "final_answer" {
		t.Errorf("expected type final_answer, got %q", resp.Type)
	}
	if resp.Content != "The error is in line 42." {
		t.Errorf("unexpected content: %q", resp.Content)
	}
}

func TestParseResponse_ToolCall(t *testing.T) {
	raw := `{"type":"tool_call","tool":"log_search","args":{"query":"err"}}`
	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Type != "tool_call" {
		t.Errorf("expected type tool_call, got %q", resp.Type)
	}
	if resp.Tool != "log_search" {
		t.Errorf("expected tool log_search, got %q", resp.Tool)
	}
	q, ok := resp.Args["query"]
	if !ok {
		t.Fatal("expected args to contain 'query'")
	}
	if q != "err" {
		t.Errorf("expected query arg 'err', got %v", q)
	}
}

func TestParseResponse_MalformedJSON(t *testing.T) {
	// Trailing garbage after valid JSON — repair via brace extraction
	raw := `Sure! Here is the response: {"type":"final_answer","content":"hello"} some trailing garbage`
	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Type != "final_answer" {
		t.Errorf("expected type final_answer, got %q", resp.Type)
	}
	if resp.Content != "hello" {
		t.Errorf("unexpected content: %q", resp.Content)
	}
}

func TestParseResponse_InvalidJSON(t *testing.T) {
	raw := `this is total garbage with no json at all`
	_, err := ParseResponse(raw)
	if err == nil {
		t.Fatal("expected error for total garbage input")
	}
}

func TestParseResponse_UnknownType(t *testing.T) {
	raw := `{"type":"unknown","content":"hmm"}`
	_, err := ParseResponse(raw)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}
