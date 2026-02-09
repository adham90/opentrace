package llm

import (
	"fmt"

	"github.com/opentrace/opentrace/internal/config"
)

// NewLLMProvider creates an LLMProvider based on config.
func NewLLMProvider(cfg *config.Config) (LLMProvider, error) {
	switch cfg.LLMProvider {
	case "ollama":
		return NewOllamaProvider(
			cfg.OllamaURL,
			cfg.OllamaModel,
			cfg.EmbeddingModel,
			cfg.EmbeddingDimension,
		), nil
	case "anthropic":
		if cfg.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("OPENTRACE_ANTHROPIC_API_KEY is required for anthropic provider")
		}
		return NewAnthropicProvider(cfg.AnthropicURL, cfg.AnthropicModel, cfg.AnthropicAPIKey), nil
	case "openai":
		if cfg.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("OPENTRACE_OPENAI_API_KEY is required for openai provider")
		}
		return NewOpenAIProvider(
			cfg.OpenAIURL,
			cfg.OpenAIModel,
			cfg.EmbeddingModel,
			cfg.EmbeddingDimension,
			cfg.OpenAIAPIKey,
		), nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %q", cfg.LLMProvider)
	}
}

// NewEmbeddingProvider creates an EmbeddingProvider based on config.
func NewEmbeddingProvider(cfg *config.Config) (EmbeddingProvider, error) {
	switch cfg.EmbeddingProvider {
	case "ollama":
		return NewOllamaProvider(
			cfg.OllamaURL,
			cfg.OllamaModel,
			cfg.EmbeddingModel,
			cfg.EmbeddingDimension,
		), nil
	case "openai":
		if cfg.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("OPENTRACE_OPENAI_API_KEY is required for openai embedding provider")
		}
		return NewOpenAIProvider(
			cfg.OpenAIURL,
			cfg.OpenAIModel,
			cfg.EmbeddingModel,
			cfg.EmbeddingDimension,
			cfg.OpenAIAPIKey,
		), nil
	default:
		return nil, fmt.Errorf("unsupported embedding provider: %q", cfg.EmbeddingProvider)
	}
}

// ProviderInfo describes an available LLM provider.
type ProviderInfo struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}

// AvailableProviders returns a list of LLM providers that have valid
// configuration. Ollama is always included. Anthropic and OpenAI require
// their respective API keys.
func AvailableProviders(cfg *config.Config) []ProviderInfo {
	var providers []ProviderInfo

	// Ollama is always available (runs locally)
	providers = append(providers, ProviderInfo{
		Name:  "ollama",
		Model: cfg.OllamaModel,
	})

	if cfg.AnthropicAPIKey != "" {
		providers = append(providers, ProviderInfo{
			Name:  "anthropic",
			Model: cfg.AnthropicModel,
		})
	}

	if cfg.OpenAIAPIKey != "" {
		providers = append(providers, ProviderInfo{
			Name:  "openai",
			Model: cfg.OpenAIModel,
		})
	}

	return providers
}
