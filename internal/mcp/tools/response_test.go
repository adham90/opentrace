package tools

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// JSONResult
// ---------------------------------------------------------------------------

func TestJSONResult_Map(t *testing.T) {
	resp := map[string]any{"status": "ok", "count": 42}
	result, err := JSONResult(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}
	text := extractTextContent(t, result)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if parsed["status"] != "ok" {
		t.Errorf("status = %v, want 'ok'", parsed["status"])
	}
}

func TestJSONResult_MapWithSuggestions(t *testing.T) {
	resp := map[string]any{"data": "test"}
	result, err := JSONResult(resp,
		Suggest("logs", "Check logs", map[string]any{"action": "search"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractTextContent(t, result)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if _, ok := parsed["suggested_tools"]; !ok {
		t.Error("expected suggested_tools in response")
	}
}

func TestJSONResult_Struct(t *testing.T) {
	type testResp struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	result, err := JSONResult(testResp{Name: "test", Count: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractTextContent(t, result)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if parsed["name"] != "test" {
		t.Errorf("name = %v, want 'test'", parsed["name"])
	}
}

func TestJSONResult_StructWithSuggestions(t *testing.T) {
	type testResp struct {
		Value string `json:"value"`
	}
	result, err := JSONResult(testResp{Value: "hello"},
		Suggest("errors", "View errors", nil),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractTextContent(t, result)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if parsed["value"] != "hello" {
		t.Errorf("value = %v, want 'hello'", parsed["value"])
	}
	if _, ok := parsed["suggested_tools"]; !ok {
		t.Error("expected suggested_tools in struct response")
	}
}

func TestJSONResult_NoSuggestions(t *testing.T) {
	resp := map[string]any{"x": 1}
	result, err := JSONResult(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractTextContent(t, result)
	var parsed map[string]any
	json.Unmarshal([]byte(text), &parsed)
	if _, ok := parsed["suggested_tools"]; ok {
		t.Error("should not have suggested_tools when none provided")
	}
}

// ---------------------------------------------------------------------------
// EmptyResult
// ---------------------------------------------------------------------------

func TestEmptyResult(t *testing.T) {
	result, err := EmptyResult("No data found.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("EmptyResult should not be an error result")
	}
	text := extractTextContent(t, result)
	if text != "No data found." {
		t.Errorf("got %q, want %q", text, "No data found.")
	}
}

// ---------------------------------------------------------------------------
// Truncate
// ---------------------------------------------------------------------------

func TestTruncate_Short(t *testing.T) {
	if got := Truncate("hello", 10); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestTruncate_Exact(t *testing.T) {
	if got := Truncate("hello", 5); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestTruncate_Long(t *testing.T) {
	got := Truncate("hello world this is long", 10)
	if got != "hello w..." {
		t.Errorf("got %q, want %q", got, "hello w...")
	}
	if len(got) != 10 {
		t.Errorf("length = %d, want 10", len(got))
	}
}

func TestTruncate_VeryShortMax(t *testing.T) {
	got := Truncate("hello", 2)
	if got != "he" {
		t.Errorf("got %q, want %q", got, "he")
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func extractTextContent(t *testing.T, result *CallToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	// Use type assertion through the mcp package's TextContent.
	type textContent interface {
		GetText() string
	}
	// The Content is *mcp.TextContent which has a Text field.
	// Access it via JSON round-trip since the interface doesn't expose GetText.
	data, _ := json.Marshal(result.Content[0])
	var tc struct {
		Text string `json:"text"`
	}
	json.Unmarshal(data, &tc)
	return tc.Text
}
