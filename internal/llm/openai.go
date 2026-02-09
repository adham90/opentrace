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

// OpenAIProvider implements LLMProvider and EmbeddingProvider using the OpenAI API.
type OpenAIProvider struct {
	baseURL        string
	model          string
	embeddingModel string
	dimension      int
	apiKey         string
	client         *http.Client
}

// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider(baseURL, model, embeddingModel string, dimension int, apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		baseURL:        baseURL,
		model:          model,
		embeddingModel: embeddingModel,
		dimension:      dimension,
		apiKey:         apiKey,
		client:         &http.Client{Timeout: 120 * time.Second},
	}
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponseFormat struct {
	Type string `json:"type"`
}

type openAIChatRequest struct {
	Model          string                `json:"model"`
	Messages       []openAIMessage       `json:"messages"`
	ResponseFormat *openAIResponseFormat  `json:"response_format,omitempty"`
	MaxTokens      int                   `json:"max_tokens,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// ChatCompletion sends a chat request to the OpenAI Chat Completions API.
func (o *OpenAIProvider) ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	var messages []openAIMessage
	for _, m := range req.Messages {
		messages = append(messages, openAIMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	openAIReq := openAIChatRequest{
		Model:    o.model,
		Messages: messages,
	}

	if req.MaxTokens > 0 {
		openAIReq.MaxTokens = req.MaxTokens
	}

	if req.JSONMode {
		openAIReq.ResponseFormat = &openAIResponseFormat{Type: "json_object"}
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

	return ChatResponse{Content: openAIResp.Choices[0].Message.Content}, nil
}

type openAIEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// Embed generates an embedding vector using the OpenAI Embeddings API.
func (o *OpenAIProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	body, err := json.Marshal(openAIEmbedRequest{
		Model: o.embeddingModel,
		Input: text,
	})
	if err != nil {
		return nil, fmt.Errorf("openai: marshal embed request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create embed request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("openai: embed returned status %d: %s", resp.StatusCode, string(errBody))
	}

	var embedResp openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("openai: decode embed response: %w", err)
	}

	if len(embedResp.Data) == 0 {
		return nil, fmt.Errorf("openai: empty embeddings response")
	}

	return embedResp.Data[0].Embedding, nil
}

// Dimension returns the configured embedding dimension.
func (o *OpenAIProvider) Dimension() int {
	return o.dimension
}
