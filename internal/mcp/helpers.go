package mcp

// ToolSuggestion recommends a follow-up tool call with pre-filled arguments.
type ToolSuggestion struct {
	Tool       string         `json:"tool"`
	Why        string         `json:"why,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Source     string         `json:"ranking_source,omitempty"`
	Evidence   string         `json:"evidence,omitempty"`
}

// suggest creates a static tool suggestion.
func suggest(tool, why string, args map[string]any) ToolSuggestion {
	return ToolSuggestion{Tool: tool, Why: why, Args: args, Source: "static"}
}

// withSuggestions adds suggested_tools to a response map.
func withSuggestions(resp map[string]any, suggestions ...ToolSuggestion) map[string]any {
	if len(suggestions) > 0 {
		resp["suggested_tools"] = suggestions
	}
	return resp
}

// truncate returns the first n characters of s with "..." appended if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

