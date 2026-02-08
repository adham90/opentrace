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
	default:
		return nil, fmt.Errorf("unsupported embedding provider: %q", cfg.EmbeddingProvider)
	}
}
