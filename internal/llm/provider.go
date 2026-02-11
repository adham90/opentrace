package llm

import (
	"context"
	"net"
	"net/http"
	"time"
)

// ChatMessage represents a single message in a conversation.
type ChatMessage struct {
	Role       string     `json:"role"`    // "system", "user", "assistant", "tool"
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`  // for assistant messages with tool use
	ToolCallID string     `json:"tool_call_id,omitempty"` // for tool result messages
}

// ToolDef describes a tool the LLM can call.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  []ToolParamDef  `json:"parameters"`
}

// ToolParamDef describes a parameter within a tool definition.
type ToolParamDef struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "string", "integer", "boolean"
	Required bool   `json:"required"`
}

// ToolCall represents a tool invocation returned by the LLM.
type ToolCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// ChatRequest is the input to an LLM chat completion call.
type ChatRequest struct {
	Messages  []ChatMessage `json:"messages"`
	Tools     []ToolDef     `json:"tools,omitempty"`
	JSONMode  bool          `json:"json_mode"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

// ChatResponse is the output from an LLM chat completion call.
type ChatResponse struct {
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	StopReason string     `json:"stop_reason,omitempty"` // "end_turn", "tool_use", "stop", etc.
}

// LLMProvider abstracts chat completion across different LLM backends.
type LLMProvider interface {
	ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// sharedTransport is a shared HTTP transport for all LLM provider HTTP clients.
// This enables connection pooling and reuse across providers.
var sharedTransport = &http.Transport{
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 10,
	IdleConnTimeout:     90 * time.Second,
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
}

// newHTTPClient returns an HTTP client using the shared transport.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   120 * time.Second,
		Transport: sharedTransport,
	}
}
