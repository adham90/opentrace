package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AnthropicProvider implements LLMProvider using the Anthropic Messages API.
type AnthropicProvider struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(baseURL, model, apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		client:  newHTTPClient(),
	}
}

// --- Anthropic API types ---

type anthropicContentBlock struct {
	Type  string          `json:"type"`            // "text" or "tool_use"
	Text  string          `json:"text,omitempty"`  // for type=text
	ID    string          `json:"id,omitempty"`    // for type=tool_use
	Name  string          `json:"name,omitempty"`  // for type=tool_use
	Input json.RawMessage `json:"input,omitempty"` // for type=tool_use
}

type anthropicMessage struct {
	Role    string        `json:"role"`
	Content any           `json:"content"` // string or []anthropicContentBlock
}

type anthropicToolInputSchema struct {
	Type       string                          `json:"type"` // "object"
	Properties map[string]anthropicToolProp    `json:"properties,omitempty"`
	Required   []string                        `json:"required,omitempty"`
}

type anthropicToolProp struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type anthropicToolDef struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	InputSchema anthropicToolInputSchema `json:"input_schema"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []anthropicMessage `json:"messages"`
	System    string             `json:"system,omitempty"`
	MaxTokens int                `json:"max_tokens"`
	Tools     []anthropicToolDef `json:"tools,omitempty"`
}

type anthropicResponse struct {
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
}

// ChatCompletion sends a chat request to the Anthropic Messages API.
func (a *AnthropicProvider) ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	var systemParts []string
	var messages []anthropicMessage

	for _, m := range req.Messages {
		if m.Role == "system" {
			if m.Content != "" {
				systemParts = append(systemParts, m.Content)
			}
			continue
		}

		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			// Assistant message with tool calls → content blocks
			var blocks []anthropicContentBlock
			if m.Content != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				inputJSON, _ := json.Marshal(tc.Args)
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: json.RawMessage(inputJSON),
				})
			}
			messages = append(messages, anthropicMessage{Role: "assistant", Content: blocks})
		} else if m.Role == "tool" {
			// Tool result → user message with tool_result content block
			block := []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     m.Content,
			}}
			messages = append(messages, anthropicMessage{Role: "user", Content: block})
		} else if m.Content != "" {
			messages = append(messages, anthropicMessage{Role: m.Role, Content: m.Content})
		}
	}

	// Merge adjacent same-role messages
	messages = mergeAdjacentAnthropicMessages(messages)

	if len(messages) == 0 {
		messages = []anthropicMessage{{Role: "user", Content: "Hello."}}
	}
	// Anthropic requires first message to be user role
	if msg, ok := messages[0].Content.(string); ok && messages[0].Role != "user" {
		_ = msg
		messages = append([]anthropicMessage{{Role: "user", Content: "Continue."}}, messages...)
	} else if messages[0].Role != "user" {
		messages = append([]anthropicMessage{{Role: "user", Content: "Continue."}}, messages...)
	}

	systemPrompt := strings.Join(systemParts, "\n\n")

	// JSON mode reinforcement (for non-tool-calling requests)
	if req.JSONMode && len(req.Tools) == 0 {
		reinforcement := "IMPORTANT: You must respond with valid JSON only. No markdown, no explanation, just JSON."
		if systemPrompt != "" {
			systemPrompt += "\n\n" + reinforcement
		} else {
			systemPrompt = reinforcement
		}
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	anthropicReq := anthropicRequest{
		Model:     a.model,
		Messages:  messages,
		System:    systemPrompt,
		MaxTokens: maxTokens,
	}

	// Convert tools
	if len(req.Tools) > 0 {
		anthropicReq.Tools = make([]anthropicToolDef, len(req.Tools))
		for i, t := range req.Tools {
			props := make(map[string]anthropicToolProp, len(t.Parameters))
			var required []string
			for _, p := range t.Parameters {
				jsonType := p.Type
				if jsonType == "int" {
					jsonType = "integer"
				}
				if jsonType == "bool" {
					jsonType = "boolean"
				}
				props[p.Name] = anthropicToolProp{Type: jsonType}
				if p.Required {
					required = append(required, p.Name)
				}
			}
			anthropicReq.Tools[i] = anthropicToolDef{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: anthropicToolInputSchema{
					Type:       "object",
					Properties: props,
					Required:   required,
				},
			}
		}
	}

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("anthropic: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("anthropic: chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return ChatResponse{}, fmt.Errorf("anthropic: chat returned status %d: %s", resp.StatusCode, string(errBody))
	}

	var anthropicResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return ChatResponse{}, fmt.Errorf("anthropic: decode response: %w", err)
	}

	// Parse content blocks into our unified response
	var result ChatResponse
	result.StopReason = anthropicResp.StopReason

	for _, block := range anthropicResp.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "tool_use":
			var args map[string]any
			if len(block.Input) > 0 {
				json.Unmarshal(block.Input, &args)
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:   block.ID,
				Name: block.Name,
				Args: args,
			})
		}
	}

	return result, nil
}

// mergeAdjacentAnthropicMessages combines consecutive messages with the same role
// when both have string content. Messages with structured content (tool results) are not merged.
func mergeAdjacentAnthropicMessages(msgs []anthropicMessage) []anthropicMessage {
	if len(msgs) <= 1 {
		return msgs
	}

	var merged []anthropicMessage
	current := msgs[0]

	for i := 1; i < len(msgs); i++ {
		curStr, curIsStr := current.Content.(string)
		nextStr, nextIsStr := msgs[i].Content.(string)

		if msgs[i].Role == current.Role && curIsStr && nextIsStr {
			current.Content = curStr + "\n\n" + nextStr
		} else {
			merged = append(merged, current)
			current = msgs[i]
		}
	}
	merged = append(merged, current)
	return merged
}
