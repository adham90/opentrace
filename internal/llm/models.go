package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/config"
)

// ModelRegistry fetches and caches available models from provider APIs.
// It queries Ollama, OpenAI, and Gemini list-models endpoints and falls
// back to the hardcoded ModelVariant lists when an API is unreachable.
type ModelRegistry struct {
	mu        sync.RWMutex
	cfg       *config.Config
	cache     []ProviderInfo
	lastFetch time.Time
	ttl       time.Duration
	client    *http.Client
}

// NewModelRegistry creates a ModelRegistry with a 5-minute cache TTL.
func NewModelRegistry(cfg *config.Config) *ModelRegistry {
	return &ModelRegistry{
		cfg:    cfg,
		ttl:    5 * time.Minute,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// ListModels returns all available models across configured providers.
// Results are cached for the TTL duration.
func (mr *ModelRegistry) ListModels(ctx context.Context) []ProviderInfo {
	mr.mu.RLock()
	if time.Since(mr.lastFetch) < mr.ttl && mr.cache != nil {
		defer mr.mu.RUnlock()
		return mr.cache
	}
	mr.mu.RUnlock()

	mr.mu.Lock()
	defer mr.mu.Unlock()

	// Double-check after acquiring write lock
	if time.Since(mr.lastFetch) < mr.ttl && mr.cache != nil {
		return mr.cache
	}

	var models []ProviderInfo

	// Ollama — always attempt (local)
	ollamaModels := mr.fetchOllamaModels(ctx)
	if len(ollamaModels) > 0 {
		models = append(models, ollamaModels...)
	} else {
		// Fallback: just the configured default model
		models = append(models, ProviderInfo{
			Name:  "ollama",
			Model: mr.cfg.OllamaModel,
		})
	}

	// Anthropic — no list API, always hardcoded
	if mr.cfg.AnthropicAPIKey != "" {
		for _, m := range AnthropicModels {
			models = append(models, ProviderInfo{
				Name:  m.Name,
				Model: m.Label,
			})
		}
	}

	// OpenAI — dynamic with fallback
	if mr.cfg.OpenAIAPIKey != "" {
		openaiModels := mr.fetchOpenAIModels(ctx)
		if len(openaiModels) > 0 {
			models = append(models, openaiModels...)
		} else {
			for _, m := range OpenAIModels {
				models = append(models, ProviderInfo{
					Name:  m.Name,
					Model: m.Label,
				})
			}
		}
	}

	// Gemini — dynamic with fallback
	if mr.cfg.GeminiAPIKey != "" {
		geminiModels := mr.fetchGeminiModels(ctx)
		if len(geminiModels) > 0 {
			models = append(models, geminiModels...)
		} else {
			for _, m := range GeminiModels {
				models = append(models, ProviderInfo{
					Name:  m.Name,
					Model: m.Label,
				})
			}
		}
	}

	mr.cache = models
	mr.lastFetch = time.Now()
	return models
}

// fetchOllamaModels calls GET {baseURL}/api/tags to list locally available models.
func (mr *ModelRegistry) fetchOllamaModels(ctx context.Context) []ProviderInfo {
	reqURL := mr.cfg.OllamaURL + "/api/tags"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		slog.Warn("ollama request error", "error", err)
		return nil
	}

	resp, err := mr.client.Do(req)
	if err != nil {
		slog.Warn("ollama unreachable", "error", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("ollama unexpected status", "status", resp.StatusCode)
		return nil
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Warn("ollama decode error", "error", err)
		return nil
	}

	var models []ProviderInfo
	for _, m := range result.Models {
		name := m.Name
		models = append(models, ProviderInfo{
			Name:  "ollama:" + name,
			Model: "Ollama - " + name,
		})
	}
	return models
}

// openAIChatModelPrefixes are prefixes for OpenAI models that support chat completions.
var openAIChatModelPrefixes = []string{
	"gpt-4", "gpt-3.5", "o1", "o3", "o4", "chatgpt",
}

// fetchOpenAIModels calls GET {baseURL}/v1/models to list available models.
// Only chat-capable models are returned (filtered by known prefixes).
func (mr *ModelRegistry) fetchOpenAIModels(ctx context.Context) []ProviderInfo {
	reqURL := mr.cfg.OpenAIURL + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		slog.Warn("openai request error", "error", err)
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+mr.cfg.OpenAIAPIKey)

	resp, err := mr.client.Do(req)
	if err != nil {
		slog.Warn("openai unreachable", "error", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		slog.Warn("openai unexpected status", "status", resp.StatusCode, "body", string(body))
		return nil
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Warn("openai decode error", "error", err)
		return nil
	}

	var models []ProviderInfo
	for _, m := range result.Data {
		if !isOpenAIChatModel(m.ID) {
			continue
		}
		models = append(models, ProviderInfo{
			Name:  "openai:" + m.ID,
			Model: "OpenAI - " + m.ID,
		})
	}
	return models
}

func isOpenAIChatModel(id string) bool {
	for _, prefix := range openAIChatModelPrefixes {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

// fetchGeminiModels calls GET {baseURL}/v1beta/models to list available models.
// Only generative models are returned.
func (mr *ModelRegistry) fetchGeminiModels(ctx context.Context) []ProviderInfo {
	reqURL := fmt.Sprintf("%s/v1beta/models?key=%s", mr.cfg.GeminiURL, url.QueryEscape(mr.cfg.GeminiAPIKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		slog.Warn("gemini request error", "error", err)
		return nil
	}

	resp, err := mr.client.Do(req)
	if err != nil {
		slog.Warn("gemini unreachable", "error", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		slog.Warn("gemini unexpected status", "status", resp.StatusCode, "body", string(body))
		return nil
	}

	var result struct {
		Models []struct {
			Name                       string   `json:"name"`        // "models/gemini-2.5-flash-preview-04-17"
			DisplayName                string   `json:"displayName"` // "Gemini 2.5 Flash Preview 04-17"
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Warn("gemini decode error", "error", err)
		return nil
	}

	var models []ProviderInfo
	for _, m := range result.Models {
		// Only include models that support generateContent (chat)
		if !supportsGenerateContent(m.SupportedGenerationMethods) {
			continue
		}

		// Strip "models/" prefix to get the model ID used in API calls
		modelID := strings.TrimPrefix(m.Name, "models/")
		label := m.DisplayName
		if label == "" {
			label = modelID
		}

		models = append(models, ProviderInfo{
			Name:  "gemini:" + modelID,
			Model: "Gemini - " + label,
		})
	}
	return models
}

func supportsGenerateContent(methods []string) bool {
	for _, m := range methods {
		if m == "generateContent" {
			return true
		}
	}
	return false
}
