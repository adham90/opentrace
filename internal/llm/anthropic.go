package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
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
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []anthropicMessage `json:"messages"`
	System    string             `json:"system,omitempty"`
	MaxTokens int                `json:"max_tokens"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// ChatCompletion sends a chat request to the Anthropic Messages API.
func (a *AnthropicProvider) ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	// Extract system messages into top-level system field
	var systemParts []string
	var messages []anthropicMessage

	for _, m := range req.Messages {
		if m.Role == "system" {
			if m.Content != "" {
				systemParts = append(systemParts, m.Content)
			}
		} else if m.Content != "" {
			// Skip empty-content messages (Anthropic rejects them)
			messages = append(messages, anthropicMessage{
				Role:    m.Role,
				Content: m.Content,
			})
		}
	}

	// Merge adjacent same-role messages (Anthropic requires strict alternation)
	messages = mergeAdjacentMessages(messages)

	// Ensure we have at least one message (Anthropic requires non-empty messages)
	if len(messages) == 0 {
		messages = []anthropicMessage{{Role: "user", Content: "Hello."}}
	}

	// Ensure messages start with a user message (Anthropic requirement)
	if messages[0].Role != "user" {
		messages = append([]anthropicMessage{{Role: "user", Content: "Continue."}}, messages...)
	}

	// Ensure messages end with a user message (Anthropic requirement)
	if messages[len(messages)-1].Role != "user" {
		messages = append(messages, anthropicMessage{Role: "user", Content: "Continue."})
	}

	systemPrompt := strings.Join(systemParts, "\n\n")

	// JSON mode: reinforce in system prompt since Anthropic has no native JSON mode
	if req.JSONMode {
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

	// Extract text from first text content block
	for _, block := range anthropicResp.Content {
		if block.Type == "text" {
			return ChatResponse{Content: block.Text}, nil
		}
	}

	return ChatResponse{}, fmt.Errorf("anthropic: no text content in response")
}

// mergeAdjacentMessages combines consecutive messages with the same role.
func mergeAdjacentMessages(msgs []anthropicMessage) []anthropicMessage {
	if len(msgs) <= 1 {
		return msgs
	}

	var merged []anthropicMessage
	current := msgs[0]

	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == current.Role {
			current.Content += "\n\n" + msgs[i].Content
		} else {
			merged = append(merged, current)
			current = msgs[i]
		}
	}
	merged = append(merged, current)

	return merged
}
