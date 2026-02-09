package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GeminiProvider implements LLMProvider using the Google Gemini REST API.
type GeminiProvider struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

// NewGeminiProvider creates a new Gemini provider.
func NewGeminiProvider(baseURL, model, apiKey string) *GeminiProvider {
	return &GeminiProvider{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// --- Gemini API types ---

type geminiPart struct {
	Text             string                `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall   `json:"functionCall,omitempty"`
	FunctionResponse *geminiToolResultPart `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiToolResultPart struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiSystemInstruction struct {
	Parts []geminiPart `json:"parts"`
}

type geminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiToolDef struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiRequest struct {
	Contents          []geminiContent          `json:"contents"`
	SystemInstruction *geminiSystemInstruction  `json:"systemInstruction,omitempty"`
	Tools             []geminiToolDef           `json:"tools,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
}

// ChatCompletion sends a chat request to the Gemini generateContent API.
func (g *GeminiProvider) ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	var systemParts []string
	var contents []geminiContent

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			if m.Content != "" {
				systemParts = append(systemParts, m.Content)
			}

		case "assistant":
			var parts []geminiPart
			if m.Content != "" {
				parts = append(parts, geminiPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Name,
						Args: tc.Args,
					},
				})
			}
			if len(parts) > 0 {
				contents = append(contents, geminiContent{Role: "model", Parts: parts})
			}

		case "tool":
			// Tool results become functionResponse parts with role "user"
			contents = append(contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResponse: &geminiToolResultPart{
						Name:     m.ToolCallID,
						Response: map[string]any{"result": m.Content},
					},
				}},
			})

		case "user":
			if m.Content != "" {
				contents = append(contents, geminiContent{
					Role: "user",
					Parts: []geminiPart{{Text: m.Content}},
				})
			}
		}
	}

	geminiReq := geminiRequest{
		Contents: contents,
	}

	// System instruction
	if len(systemParts) > 0 {
		systemText := strings.Join(systemParts, "\n\n")
		// JSON mode reinforcement for non-tool-calling requests
		if req.JSONMode && len(req.Tools) == 0 {
			systemText += "\n\nIMPORTANT: You must respond with valid JSON only. No markdown, no explanation, just JSON."
		}
		geminiReq.SystemInstruction = &geminiSystemInstruction{
			Parts: []geminiPart{{Text: systemText}},
		}
	} else if req.JSONMode && len(req.Tools) == 0 {
		geminiReq.SystemInstruction = &geminiSystemInstruction{
			Parts: []geminiPart{{Text: "IMPORTANT: You must respond with valid JSON only. No markdown, no explanation, just JSON."}},
		}
	}

	// Convert tools
	if len(req.Tools) > 0 {
		var decls []geminiFunctionDeclaration
		for _, t := range req.Tools {
			params := buildJSONSchema(t.Parameters)
			decls = append(decls, geminiFunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			})
		}
		geminiReq.Tools = []geminiToolDef{{FunctionDeclarations: decls}}
	}

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("gemini: marshal request: %w", err)
	}

	reqURL := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", g.baseURL, g.model, url.QueryEscape(g.apiKey))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("gemini: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("gemini: chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return ChatResponse{}, fmt.Errorf("gemini: chat returned status %d: %s", resp.StatusCode, string(errBody))
	}

	var geminiResp geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return ChatResponse{}, fmt.Errorf("gemini: decode response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 {
		return ChatResponse{}, fmt.Errorf("gemini: no candidates in response")
	}

	candidate := geminiResp.Candidates[0]
	result := ChatResponse{
		StopReason: candidate.FinishReason,
	}

	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			result.Content += part.Text
		}
		if part.FunctionCall != nil {
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:   part.FunctionCall.Name, // Gemini uses function name as ID
				Name: part.FunctionCall.Name,
				Args: part.FunctionCall.Args,
			})
		}
	}

	return result, nil
}
