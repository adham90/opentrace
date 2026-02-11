package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/adham90/opentrace/internal/llm"
)

// RunConfig holds limits for the agent loop.
type RunConfig struct {
	MaxSteps            int
	MaxToolCalls        int
	MaxObservationBytes int
}

// Event represents an observable event from the agent loop.
type Event struct {
	Type     string         // "thinking", "tool_call", "observation", "final", "error"
	Content  string
	ToolName string
	Args     map[string]any
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

	// Build tool lookup map and LLM tool definitions
	toolMap := make(map[string]Tool, len(tools))
	var toolDefs []llm.ToolDef
	for _, t := range tools {
		toolMap[t.Name] = t
		var params []llm.ToolParamDef
		for _, p := range t.Params {
			params = append(params, llm.ToolParamDef{
				Name:     p.Name,
				Type:     p.Type,
				Required: p.Required,
			})
		}
		toolDefs = append(toolDefs, llm.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		})
	}

	// Build initial messages
	systemPrompt := BuildSystemPrompt(tools)
	messages := []llm.ChatMessage{
		{Role: "system", Content: systemPrompt},
	}
	if len(history) > 0 {
		messages = append(messages, history...)
	}
	messages = append(messages, llm.ChatMessage{Role: "user", Content: query})

	toolCallCount := 0

	for step := 0; step < a.cfg.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("agent: %w", err)
		}

		// Call LLM with native tool definitions
		resp, err := a.llm.ChatCompletion(ctx, llm.ChatRequest{
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			return "", fmt.Errorf("agent: llm call failed: %w", err)
		}

		// Emit thinking if the LLM returned text content
		if resp.Content != "" {
			emit(Event{Type: "thinking", Content: resp.Content})
		}

		// No tool calls → treat text content as final answer
		if len(resp.ToolCalls) == 0 {
			if resp.Content == "" {
				return "", fmt.Errorf("agent: empty response from LLM (no content and no tool calls)")
			}
			emit(Event{Type: "final", Content: resp.Content})
			return resp.Content, nil
		}

		// Process each tool call in the response
		// First, append the assistant message with tool calls to history
		messages = append(messages, llm.ChatMessage{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute tool calls concurrently when multiple are requested
		type toolResult struct {
			index   int
			message llm.ChatMessage
		}

		// Pre-validate all tool calls and check budget
		validCalls := make([]struct {
			tc   llm.ToolCall
			tool Tool
		}, 0, len(resp.ToolCalls))

		for _, tc := range resp.ToolCalls {
			tool, ok := toolMap[tc.Name]
			if !ok {
				errMsg := fmt.Sprintf("Unknown tool %q. Available: %s", tc.Name, toolNames(tools))
				emit(Event{Type: "error", Content: errMsg})
				messages = append(messages, llm.ChatMessage{
					Role: "tool", Content: errMsg, ToolCallID: tc.ID,
				})
				continue
			}

			if err := ValidateArgs(tool.Params, tc.Args); err != nil {
				errMsg := fmt.Sprintf("Invalid arguments for tool %q: %s", tc.Name, err)
				emit(Event{Type: "error", Content: errMsg})
				messages = append(messages, llm.ChatMessage{
					Role: "tool", Content: errMsg, ToolCallID: tc.ID,
				})
				continue
			}

			if toolCallCount >= a.cfg.MaxToolCalls {
				return "", fmt.Errorf("agent: tool call budget exhausted (%d calls)", a.cfg.MaxToolCalls)
			}
			toolCallCount++
			validCalls = append(validCalls, struct {
				tc   llm.ToolCall
				tool Tool
			}{tc, tool})
		}

		if len(validCalls) == 1 {
			// Single tool call — execute inline (no goroutine overhead)
			vc := validCalls[0]
			emit(Event{Type: "tool_call", ToolName: vc.tc.Name, Args: vc.tc.Args})
			result, err := vc.tool.Handler(ctx, vc.tc.Args)
			if err != nil {
				result = fmt.Sprintf("Tool error: %s", err)
			}
			result = TruncateObservation(result, a.cfg.MaxObservationBytes)
			emit(Event{Type: "observation", Content: result, ToolName: vc.tc.Name})
			messages = append(messages, llm.ChatMessage{
				Role: "tool", Content: result, ToolCallID: vc.tc.ID,
			})
		} else if len(validCalls) > 1 {
			// Multiple tool calls — execute concurrently
			results := make([]toolResult, len(validCalls))
			var wg sync.WaitGroup
			for i, vc := range validCalls {
				emit(Event{Type: "tool_call", ToolName: vc.tc.Name, Args: vc.tc.Args})
				wg.Add(1)
				go func(idx int, tc llm.ToolCall, tool Tool) {
					defer wg.Done()
					result, err := tool.Handler(ctx, tc.Args)
					if err != nil {
						result = fmt.Sprintf("Tool error: %s", err)
					}
					result = TruncateObservation(result, a.cfg.MaxObservationBytes)
					results[idx] = toolResult{
						index: idx,
						message: llm.ChatMessage{
							Role: "tool", Content: result, ToolCallID: tc.ID,
						},
					}
				}(i, vc.tc, vc.tool)
			}
			wg.Wait()

			// Append results in order and emit observations
			for i, r := range results {
				emit(Event{Type: "observation", Content: r.message.Content, ToolName: validCalls[i].tc.Name})
				messages = append(messages, r.message)
			}
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
