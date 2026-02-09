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

func TestParseResponse_UnknownType_RecoveredAsToolCall(t *testing.T) {
	// LLM sometimes puts tool name in "type" field — should recover as tool_call
	raw := `{"type":"db_search","args":{"query":"SELECT 1"}}`
	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Type != "tool_call" {
		t.Errorf("expected type tool_call, got %q", resp.Type)
	}
	if resp.Tool != "db_search" {
		t.Errorf("expected tool db_search, got %q", resp.Tool)
	}
}

func TestParseResponse_EmptyType(t *testing.T) {
	raw := `{"type":"","content":"hmm"}`
	_, err := ParseResponse(raw)
	if err == nil {
		t.Fatal("expected error for empty type")
	}
}

func TestParseResponse_Plan(t *testing.T) {
	raw := `{"type":"plan","steps":[{"id":1,"description":"Search logs"},{"id":2,"description":"Query database"}]}`
	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Type != "plan" {
		t.Errorf("expected type plan, got %q", resp.Type)
	}
	if len(resp.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(resp.Steps))
	}
	if resp.Steps[0].ID != 1 || resp.Steps[0].Description != "Search logs" {
		t.Errorf("unexpected step 0: %+v", resp.Steps[0])
	}
	if resp.Steps[1].ID != 2 || resp.Steps[1].Description != "Query database" {
		t.Errorf("unexpected step 1: %+v", resp.Steps[1])
	}
}

func TestParseResponse_PlanEmptySteps(t *testing.T) {
	raw := `{"type":"plan","steps":[]}`
	_, err := ParseResponse(raw)
	if err == nil {
		t.Fatal("expected error for plan with empty steps")
	}
}

func TestParseResponse_PlanUpdate(t *testing.T) {
	raw := `{"type":"plan_update","step_id":2,"status":"completed"}`
	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Type != "plan_update" {
		t.Errorf("expected type plan_update, got %q", resp.Type)
	}
	if resp.StepID != 2 {
		t.Errorf("expected step_id 2, got %d", resp.StepID)
	}
	if resp.Status != "completed" {
		t.Errorf("expected status completed, got %q", resp.Status)
	}
}

func TestParseResponse_PlanUpdateDefaultStatus(t *testing.T) {
	raw := `{"type":"plan_update","step_id":1}`
	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "in_progress" {
		t.Errorf("expected default status in_progress, got %q", resp.Status)
	}
}

func TestParseResponse_PlanUpdateInvalidStepID(t *testing.T) {
	raw := `{"type":"plan_update","step_id":0}`
	_, err := ParseResponse(raw)
	if err == nil {
		t.Fatal("expected error for plan_update with step_id 0")
	}
}
