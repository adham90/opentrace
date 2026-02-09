package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OllamaProvider implements LLMProvider and EmbeddingProvider using the Ollama HTTP API.
type OllamaProvider struct {
	baseURL        string
	model          string
	embeddingModel string
	dimension      int
	client         *http.Client
}

// NewOllamaProvider creates a new Ollama provider.
func NewOllamaProvider(baseURL, chatModel, embeddingModel string, dimension int) *OllamaProvider {
	return &OllamaProvider{
		baseURL:        baseURL,
		model:          chatModel,
		embeddingModel: embeddingModel,
		dimension:      dimension,
		client:         &http.Client{Timeout: 120 * time.Second},
	}
}

// --- Ollama chat API types ---

type ollamaToolCall struct {
	Function struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

type ollamaChatMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolDef struct {
	Type     string       `json:"type"` // "function"
	Function ollamaFunc   `json:"function"`
}

type ollamaFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Format   string              `json:"format,omitempty"`
	Tools    []ollamaToolDef     `json:"tools,omitempty"`
}

type ollamaChatResponse struct {
	Message ollamaChatMessage `json:"message"`
}

// ChatCompletion sends a chat request to Ollama and returns the response.
func (o *OllamaProvider) ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	var messages []ollamaChatMessage
	for _, m := range req.Messages {
		msg := ollamaChatMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		messages = append(messages, msg)
	}

	ollamaReq := ollamaChatRequest{
		Model:    o.model,
		Messages: messages,
		Stream:   false,
	}

	if req.JSONMode && len(req.Tools) == 0 {
		ollamaReq.Format = "json"
	}

	// Convert tools (Ollama uses OpenAI-compatible format)
	if len(req.Tools) > 0 {
		ollamaReq.Tools = make([]ollamaToolDef, len(req.Tools))
		for i, t := range req.Tools {
			params := buildJSONSchema(t.Parameters)
			ollamaReq.Tools[i] = ollamaToolDef{
				Type: "function",
				Function: ollamaFunc{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  params,
				},
			}
		}
	}

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("ollama: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("ollama: chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ChatResponse{}, fmt.Errorf("ollama: chat returned status %d", resp.StatusCode)
	}

	var ollamaResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return ChatResponse{}, fmt.Errorf("ollama: decode chat response: %w", err)
	}

	result := ChatResponse{
		Content: ollamaResp.Message.Content,
	}

	// Parse tool calls
	for _, tc := range ollamaResp.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:   fmt.Sprintf("ollama_%s_%d", tc.Function.Name, time.Now().UnixNano()),
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}

	if len(result.ToolCalls) > 0 {
		result.StopReason = "tool_use"
	}

	return result, nil
}

// --- Embeddings ---

type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

// Embed generates an embedding vector for the given text.
func (o *OllamaProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	body, err := json.Marshal(ollamaEmbedRequest{
		Model: o.embeddingModel,
		Input: text,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal embed request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: create embed request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: embed returned status %d", resp.StatusCode)
	}

	var ollamaResp ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("ollama: decode embed response: %w", err)
	}

	if len(ollamaResp.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama: empty embeddings response")
	}

	return ollamaResp.Embeddings[0], nil
}

// Dimension returns the configured embedding dimension.
func (o *OllamaProvider) Dimension() int {
	return o.dimension
}
