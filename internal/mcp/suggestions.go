package mcp

import "context"

// ToolSuggestion recommends a follow-up tool call with pre-filled arguments.
type ToolSuggestion struct {
	Tool       string         `json:"tool"`
	Why        string         `json:"why,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`       // 0.0–1.0
	Source     string         `json:"ranking_source,omitempty"`   // "learned", "template", "static"
	Evidence   string         `json:"evidence,omitempty"`         // e.g. "Used in 5 resolved sessions"
}

// suggest is a convenience constructor.
func suggest(tool, why string, args map[string]any) ToolSuggestion {
	return ToolSuggestion{Tool: tool, Why: why, Args: args, Source: "static"}
}

// withSuggestions adds a suggested_tools array to a response map.
// If a RankingService is available, suggestions are re-ranked using learned data.
func withSuggestions(resp map[string]any, suggestions ...ToolSuggestion) map[string]any {
	if rankingService != nil && sessionTracker != nil {
		if sess := sessionTracker.CurrentSession(); sess != nil {
			currentTool := ""
			if len(sess.ToolSequence) > 0 {
				currentTool = sess.ToolSequence[len(sess.ToolSequence)-1]
			}
			ranked := rankingService.RankSuggestions(context.Background(), RankingRequest{
				CurrentTool:         currentTool,
				Intent:              sess.Intent,
				StepIndex:           sess.TotalSteps,
				SessionTools:        sess.ToolSequence,
				FallbackSuggestions: suggestions,
			})
			if len(ranked) > 0 {
				suggestions = ranked
			}
		}
	}

	if len(suggestions) > 0 {
		resp["suggested_tools"] = suggestions
		// Track for acceptance detection
		if sessionTracker != nil {
			sessionTracker.SetLastSuggestions(suggestions)
		}
	}
	return resp
}
