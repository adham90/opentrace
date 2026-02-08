package agent

import (
	"fmt"
	"strings"
)

// BuildSystemPrompt builds the dynamic system prompt for the agent.
// It includes the base instructions, response format, and tool descriptions
// for any active tools.
func BuildSystemPrompt(tools []Tool) string {
	var b strings.Builder

	b.WriteString(`You are OpenTrace, an AI debugging assistant. You help users investigate production issues by searching logs, querying databases, and reading code.

## Response Format

You MUST respond with valid JSON in one of two formats:

1. To provide a final answer:
{"type":"final_answer","content":"your answer here"}

2. To call a tool:
{"type":"tool_call","tool":"tool_name","args":{"param":"value"}}

Do NOT include any text outside the JSON object.`)

	if len(tools) > 0 {
		b.WriteString("\n\n## Available tools:\n\n")
		for _, t := range tools {
			b.WriteString(fmt.Sprintf("### %s\n%s\n", t.Name, t.Description))
			if len(t.Params) > 0 {
				b.WriteString("Parameters:\n")
				for _, p := range t.Params {
					req := "optional"
					if p.Required {
						req = "required"
					}
					b.WriteString(fmt.Sprintf("- %s (%s, %s)\n", p.Name, p.Type, req))
				}
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}
