package agent

import (
	"strings"
)

// BuildSystemPrompt builds the dynamic system prompt for the agent.
// With native tool calling, we don't need JSON format instructions —
// the LLM will use the tool definitions directly.
func BuildSystemPrompt(tools []Tool) string {
	toolSet := make(map[string]bool, len(tools))
	for _, t := range tools {
		toolSet[t.Name] = true
	}

	var b strings.Builder

	b.WriteString(`You are OpenTrace, an AI debugging assistant. You help users investigate issues by querying databases, searching logs, and reading code.

## Rules

1. Use the available tools to find information. Do NOT guess or make up data.
2. You can make multiple tool calls across steps. Call a tool, read the result, then call another or give a final answer.
3. Always base your final answer on actual tool results, not assumptions.
4. If a tool returns an error, include the exact error text so the user can debug it.
5. When you have enough information, respond with a clear, detailed answer.

## Strategy
`)

	if toolSet["db_search"] {
		b.WriteString(`- For database questions: use db_schema first to understand table structure, then write SQL with db_search.
- Look at foreign key references in db_schema output to know how to JOIN tables.
`)
	}

	if toolSet["log_search"] {
		b.WriteString(`- For log investigation: use log_search with filters. Start broad (e.g. level="error"), then narrow down.
- To trace a request: find the trace_id in logs, then search by trace_id to find all related entries.
`)
	}

	b.WriteString(`- For complex questions: break them into steps and call tools sequentially.
- If you already have information from earlier in this conversation, use it directly instead of calling tools again.`)

	if toolSet["memory_read"] {
		b.WriteString(`
- Check memory_read at the start to see what you already know about the system.
- After discovering useful facts (table relationships, data patterns, common errors), save them with memory_write.`)
	}

	b.WriteString(`
- Always provide specific numbers and data in your final answer, not vague statements.
- NEVER mention tools or internal steps in your final answer. Only include the actual data the user asked for.`)

	// Note: Tool definitions are passed via the native tool calling API,
	// so we do NOT list them again in the system prompt text. Duplicating
	// them confuses some models into thinking tools are "not available."

	return b.String()
}
