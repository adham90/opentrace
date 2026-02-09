package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PlanStep represents a single step in an agent plan.
type PlanStep struct {
	ID          int    `json:"id"`
	Description string `json:"description"`
}

// AgentResponse represents a parsed LLM response.
type AgentResponse struct {
	Type    string         `json:"type"`              // "final_answer", "tool_call", "plan", or "plan_update"
	Content string         `json:"content"`           // for final_answer
	Tool    string         `json:"tool"`              // for tool_call
	Args    map[string]any `json:"args"`              // for tool_call
	Steps   []PlanStep     `json:"steps,omitempty"`   // for "plan"
	StepID  int            `json:"step_id,omitempty"` // for "plan_update"
	Status  string         `json:"status,omitempty"`  // for "plan_update"
}

// ParseResponse parses a raw LLM response string into an AgentResponse.
// If the initial parse fails, it attempts repair by extracting the substring
// between the first '{' and last '}'.
func ParseResponse(raw string) (AgentResponse, error) {
	var resp AgentResponse

	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		// Attempt repair: extract first { to last }
		start := strings.Index(raw, "{")
		end := strings.LastIndex(raw, "}")
		if start == -1 || end == -1 || end <= start {
			return AgentResponse{}, fmt.Errorf("parse response: no valid JSON found")
		}
		sub := raw[start : end+1]
		if err := json.Unmarshal([]byte(sub), &resp); err != nil {
			return AgentResponse{}, fmt.Errorf("parse response: repair failed: %w", err)
		}
	}

	switch resp.Type {
	case "final_answer", "tool_call":
		return resp, nil
	case "plan":
		if len(resp.Steps) == 0 {
			return AgentResponse{}, fmt.Errorf("parse response: plan must have at least one step")
		}
		return resp, nil
	case "plan_update":
		if resp.StepID <= 0 {
			return AgentResponse{}, fmt.Errorf("parse response: plan_update requires step_id > 0")
		}
		if resp.Status == "" {
			resp.Status = "in_progress"
		}
		return resp, nil
	default:
		// LLM sometimes puts the tool name in the "type" field instead of "tool_call".
		// Recover by treating the type value as the tool name.
		if resp.Type != "" && resp.Type != "final_answer" {
			if resp.Tool == "" {
				resp.Tool = resp.Type
			}
			resp.Type = "tool_call"
			return resp, nil
		}
		return AgentResponse{}, fmt.Errorf("parse response: unknown type %q", resp.Type)
	}
}
