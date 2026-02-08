package llm

import "context"

// ChatMessage represents a single message in a conversation.
type ChatMessage struct {
	Role    string `json:"role"`    // "system", "user", "assistant"
	Content string `json:"content"`
}

// ChatRequest is the input to an LLM chat completion call.
type ChatRequest struct {
	Messages  []ChatMessage `json:"messages"`
	JSONMode  bool          `json:"json_mode"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

// ChatResponse is the output from an LLM chat completion call.
type ChatResponse struct {
	Content string `json:"content"`
}

// LLMProvider abstracts chat completion across different LLM backends.
type LLMProvider interface {
	ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// EmbeddingProvider abstracts text embedding across different backends.
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float64, error)
	Dimension() int
}
