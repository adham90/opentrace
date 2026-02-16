package mcp

// ToolSuggestion recommends a follow-up tool call with pre-filled arguments.
type ToolSuggestion struct {
	Tool string         `json:"tool"`
	Why  string         `json:"why,omitempty"`
	Args map[string]any `json:"args,omitempty"`
}

// suggest is a convenience constructor.
func suggest(tool, why string, args map[string]any) ToolSuggestion {
	return ToolSuggestion{Tool: tool, Why: why, Args: args}
}

// withSuggestions adds a suggested_tools array to a response map.
func withSuggestions(resp map[string]any, suggestions ...ToolSuggestion) map[string]any {
	if len(suggestions) > 0 {
		resp["suggested_tools"] = suggestions
	}
	return resp
}
