package tools

// ToolSuggestion recommends a follow-up tool call with pre-filled arguments.
type ToolSuggestion struct {
	Tool       string         `json:"tool"`
	Why        string         `json:"why,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Source     string         `json:"ranking_source,omitempty"`
	Evidence   string         `json:"evidence,omitempty"`
}

// Suggest is a convenience constructor for a static suggestion.
func Suggest(tool, why string, args map[string]any) ToolSuggestion {
	return ToolSuggestion{Tool: tool, Why: why, Args: args, Source: "static"}
}

// WithSuggestions adds a suggested_tools array to a response map.
func WithSuggestions(resp map[string]any, suggestions ...ToolSuggestion) map[string]any {
	if len(suggestions) > 0 {
		resp["suggested_tools"] = suggestions
	}
	return resp
}
