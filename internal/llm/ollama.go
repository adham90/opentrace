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

// ollamaChatRequest is the JSON body sent to POST /api/chat.
type ollamaChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Format   string        `json:"format,omitempty"`
}

// ollamaChatResponse is the JSON body returned from POST /api/chat.
type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

// ChatCompletion sends a chat request to Ollama and returns the response.
func (o *OllamaProvider) ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	ollamaReq := ollamaChatRequest{
		Model:    o.model,
		Messages: req.Messages,
		Stream:   false,
	}
	if req.JSONMode {
		ollamaReq.Format = "json"
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

	return ChatResponse{Content: ollamaResp.Message.Content}, nil
}

// ollamaEmbedRequest is the JSON body sent to POST /api/embed.
type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// ollamaEmbedResponse is the JSON body returned from POST /api/embed.
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
