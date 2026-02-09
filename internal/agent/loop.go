package agent

import (
	"context"
	"fmt"

	"github.com/opentrace/opentrace/internal/llm"
)

// RunConfig holds limits for the agent loop.
type RunConfig struct {
	MaxSteps            int
	MaxToolCalls        int
	MaxObservationBytes int
}

// Event represents an observable event from the agent loop.
type Event struct {
	Type     string         // "thinking", "tool_call", "observation", "final", "error", "plan", "plan_update"
	Content  string
	ToolName string
	Args     map[string]any
	Steps    []PlanStep // for "plan"
	StepID   int        // for "plan_update"
	Status   string     // for "plan_update"
}

// EventCallback is a function that receives agent events for observability.
type EventCallback func(Event)

// Agent orchestrates the LLM + tool loop.
type Agent struct {
	llm llm.LLMProvider
	cfg RunConfig
}

// New creates a new Agent with the given LLM provider and config.
func New(provider llm.LLMProvider, cfg RunConfig) *Agent {
	return &Agent{llm: provider, cfg: cfg}
}

// Run executes the agent loop: calls the LLM, parses responses, executes tools,
// and returns the final answer.
func (a *Agent) Run(ctx context.Context, query string, tools []Tool) (string, error) {
	return a.RunWithCallback(ctx, query, tools, nil, nil)
}

// RunWithCallback is like Run but emits events via the callback for observability.
// If history is non-nil, prior conversation messages are prepended before the current query.
func (a *Agent) RunWithCallback(ctx context.Context, query string, tools []Tool, cb EventCallback, history []llm.ChatMessage) (string, error) {
	emit := func(e Event) {
		if cb != nil {
			cb(e)
		}
	}

	// Build tool lookup map
	toolMap := make(map[string]Tool, len(tools))
	for _, t := range tools {
		toolMap[t.Name] = t
	}

	// Build initial messages
	systemPrompt := BuildSystemPrompt(tools)
	messages := []llm.ChatMessage{
		{Role: "system", Content: systemPrompt},
	}
	// Append prior conversation history if available
	if len(history) > 0 {
		messages = append(messages, history...)
	}
	messages = append(messages, llm.ChatMessage{Role: "user", Content: query})

	toolCallCount := 0

	for step := 0; step < a.cfg.MaxSteps; step++ {
		// Check context cancellation
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("agent: %w", err)
		}

		// Call LLM
		resp, err := a.llm.ChatCompletion(ctx, llm.ChatRequest{
			Messages: messages,
			JSONMode: true,
		})
		if err != nil {
			return "", fmt.Errorf("agent: llm call failed: %w", err)
		}

		emit(Event{Type: "thinking", Content: resp.Content})

		// Parse response
		parsed, err := ParseResponse(resp.Content)
		if err != nil {
			// Malformed JSON — add error to history and let LLM retry
			errMsg := fmt.Sprintf("Your response was not valid JSON. Error: %s. Please respond with valid JSON.", err)
			messages = append(messages,
				llm.ChatMessage{Role: "assistant", Content: resp.Content},
				llm.ChatMessage{Role: "user", Content: errMsg},
			)
			emit(Event{Type: "error", Content: errMsg})
			continue
		}

		switch parsed.Type {
		case "final_answer":
			emit(Event{Type: "final", Content: parsed.Content})
			return parsed.Content, nil

		case "plan":
			emit(Event{Type: "plan", Steps: parsed.Steps})
			messages = append(messages,
				llm.ChatMessage{Role: "assistant", Content: resp.Content},
				llm.ChatMessage{Role: "user", Content: "Good plan. Now execute step 1."},
			)
			step-- // free — don't consume step budget
			continue

		case "plan_update":
			emit(Event{Type: "plan_update", StepID: parsed.StepID, Status: parsed.Status})
			messages = append(messages,
				llm.ChatMessage{Role: "assistant", Content: resp.Content},
				llm.ChatMessage{Role: "user", Content: "Noted. Continue."},
			)
			step-- // free — don't consume step budget
			continue

		case "tool_call":
			// Look up tool
			tool, ok := toolMap[parsed.Tool]
			if !ok {
				errMsg := fmt.Sprintf("Unknown tool %q. You can ONLY use these tools: %s. Pick the most relevant tool from this list.", parsed.Tool, toolNames(tools))
				messages = append(messages,
					llm.ChatMessage{Role: "assistant", Content: resp.Content},
					llm.ChatMessage{Role: "user", Content: errMsg},
				)
				emit(Event{Type: "error", Content: errMsg})
				continue // doesn't burn tool budget
			}

			// Validate args
			if err := ValidateArgs(tool.Params, parsed.Args); err != nil {
				errMsg := fmt.Sprintf("Invalid arguments for tool %q: %s", parsed.Tool, err)
				messages = append(messages,
					llm.ChatMessage{Role: "assistant", Content: resp.Content},
					llm.ChatMessage{Role: "user", Content: errMsg},
				)
				emit(Event{Type: "error", Content: errMsg})
				continue // doesn't burn tool budget
			}

			// Check tool budget
			if toolCallCount >= a.cfg.MaxToolCalls {
				return "", fmt.Errorf("agent: tool call budget exhausted (%d calls)", a.cfg.MaxToolCalls)
			}
			toolCallCount++

			emit(Event{Type: "tool_call", ToolName: parsed.Tool, Args: parsed.Args})

			// Execute tool
			result, err := tool.Handler(ctx, parsed.Args)
			if err != nil {
				result = fmt.Sprintf("Tool error: %s", err)
			}

			// Truncate observation
			result = TruncateObservation(result, a.cfg.MaxObservationBytes)

			emit(Event{Type: "observation", Content: result, ToolName: parsed.Tool})

			// Append to history
			messages = append(messages,
				llm.ChatMessage{Role: "assistant", Content: resp.Content},
				llm.ChatMessage{Role: "user", Content: fmt.Sprintf("Tool %q returned:\n%s", parsed.Tool, result)},
			)
		}
	}

	return "", fmt.Errorf("agent: exceeded max steps (%d)", a.cfg.MaxSteps)
}

func toolNames(tools []Tool) string {
	if len(tools) == 0 {
		return "(none)"
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return fmt.Sprintf("%v", names)
}
