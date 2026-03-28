package tools

import (
	"encoding/json"
	"testing"
)

// extractText is a shared test helper that pulls the first text content from a CallToolResult.
func extractText(t *testing.T, result *CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected at least one content item")
	}
	// Use JSON round-trip to get the text since Content items are interfaces.
	raw, err := json.Marshal(result.Content[0])
	if err != nil {
		t.Fatalf("failed to marshal content: %v", err)
	}
	var obj struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("failed to unmarshal content: %v", err)
	}
	return obj.Text
}
