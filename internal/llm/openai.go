package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAIProvider implements LLMProvider using the OpenAI API.
type OpenAIProvider struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider(baseURL, model, apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// --- OpenAI API types ---

type openAIMessage struct {
	Role       string              `json:"role"`
	Content    string              `json:"content,omitempty"`
	ToolCalls  []openAIToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"` // "function"
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

type openAIResponseFormat struct {
	Type string `json:"type"`
}

type openAIToolDef struct {
	Type     string         `json:"type"` // "function"
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openAIChatRequest struct {
	Model          string               `json:"model"`
	Messages       []openAIMessage      `json:"messages"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
	MaxTokens      int                  `json:"max_tokens,omitempty"`
	Tools          []openAIToolDef      `json:"tools,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message      openAIMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
}

// ChatCompletion sends a chat request to the OpenAI Chat Completions API.
func (o *OpenAIProvider) ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	var messages []openAIMessage
	for _, m := range req.Messages {
		msg := openAIMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		// Forward tool calls in assistant messages
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Args)
				msg.ToolCalls = append(msg.ToolCalls, openAIToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: openAIFunctionCall{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					},
				})
			}
		}
		// Forward tool result messages
		if m.Role == "tool" {
			msg.ToolCallID = m.ToolCallID
		}
		messages = append(messages, msg)
	}

	openAIReq := openAIChatRequest{
		Model:    o.model,
		Messages: messages,
	}

	if req.MaxTokens > 0 {
		openAIReq.MaxTokens = req.MaxTokens
	}

	if req.JSONMode && len(req.Tools) == 0 {
		openAIReq.ResponseFormat = &openAIResponseFormat{Type: "json_object"}
	}

	// Convert tools
	if len(req.Tools) > 0 {
		openAIReq.Tools = make([]openAIToolDef, len(req.Tools))
		for i, t := range req.Tools {
			params := buildJSONSchema(t.Parameters)
			openAIReq.Tools[i] = openAIToolDef{
				Type: "function",
				Function: openAIFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  params,
				},
			}
		}
	}

	body, err := json.Marshal(openAIReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("openai: chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return ChatResponse{}, fmt.Errorf("openai: chat returned status %d: %s", resp.StatusCode, string(errBody))
	}

	var openAIResp openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return ChatResponse{}, fmt.Errorf("openai: decode response: %w", err)
	}

	if len(openAIResp.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("openai: no choices in response")
	}

	choice := openAIResp.Choices[0]
	result := ChatResponse{
		Content:    choice.Message.Content,
		StopReason: choice.FinishReason,
	}

	// Parse tool calls
	for _, tc := range choice.Message.ToolCalls {
		var args map[string]any
		if tc.Function.Arguments != "" {
			json.Unmarshal([]byte(tc.Function.Arguments), &args)
		}
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: args,
		})
	}

	return result, nil
}

// buildJSONSchema converts our ToolParamDef slice into a JSON Schema object.
func buildJSONSchema(params []ToolParamDef) json.RawMessage {
	props := make(map[string]map[string]string, len(params))
	var required []string
	for _, p := range params {
		jsonType := p.Type
		if jsonType == "int" {
			jsonType = "integer"
		}
		if jsonType == "bool" {
			jsonType = "boolean"
		}
		props[p.Name] = map[string]string{"type": jsonType}
		if p.Required {
			required = append(required, p.Name)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	b, _ := json.Marshal(schema)
	return json.RawMessage(b)
}
