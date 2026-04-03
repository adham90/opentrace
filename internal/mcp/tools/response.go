package tools

import "encoding/json"

// ---------------------------------------------------------------------------
// Response building helpers
//
// Every handler ends with the same 3-5 lines: attach suggestions, marshal
// JSON, return NewToolResultText. These helpers collapse that to one call.
// ---------------------------------------------------------------------------

// JSONResult marshals resp to JSON, attaches suggestions, and returns a
// successful CallToolResult. If resp is a map[string]any, suggestions are
// added inline under "suggested_tools". For struct types, resp is wrapped
// in a map so suggestions can be appended.
func JSONResult(resp any, suggestions ...ToolSuggestion) (*CallToolResult, error) {
	// If resp is already a map, add suggestions inline.
	if m, ok := resp.(map[string]any); ok {
		WithSuggestions(m, suggestions...)
		data, _ := json.Marshal(m)
		return NewToolResultText(string(data)), nil
	}

	// For struct types: marshal, then attach suggestions by re-parsing.
	// This avoids requiring every response struct to have a Suggestions field.
	if len(suggestions) == 0 {
		data, _ := json.Marshal(resp)
		return NewToolResultText(string(data)), nil
	}

	// Marshal struct to map, then add suggestions.
	data, _ := json.Marshal(resp)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		// Fallback: return without suggestions.
		return NewToolResultText(string(data)), nil
	}
	WithSuggestions(m, suggestions...)
	data, _ = json.Marshal(m)
	return NewToolResultText(string(data)), nil
}

// EmptyResult returns a text result for "no data found" scenarios.
func EmptyResult(msg string) (*CallToolResult, error) {
	return NewToolResultText(msg), nil
}

// Truncate shortens s to maxLen characters, appending "..." if truncated.
// This is the canonical truncation helper — use instead of local variants.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
