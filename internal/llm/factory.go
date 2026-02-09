package llm

import (
	"fmt"

	"github.com/adham90/opentrace/internal/config"
)

// NewLLMProvider creates an LLMProvider based on config.
func NewLLMProvider(cfg *config.Config) (LLMProvider, error) {
	switch cfg.LLMProvider {
	case "ollama":
		return NewOllamaProvider(cfg.OllamaURL, cfg.OllamaModel), nil
	case "anthropic":
		if cfg.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("OPENTRACE_ANTHROPIC_API_KEY is required for anthropic provider")
		}
		return NewAnthropicProvider(cfg.AnthropicURL, cfg.AnthropicModel, cfg.AnthropicAPIKey), nil
	case "openai":
		if cfg.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("OPENTRACE_OPENAI_API_KEY is required for openai provider")
		}
		return NewOpenAIProvider(cfg.OpenAIURL, cfg.OpenAIModel, cfg.OpenAIAPIKey), nil
	case "gemini":
		if cfg.GeminiAPIKey == "" {
			return nil, fmt.Errorf("OPENTRACE_GEMINI_API_KEY is required for gemini provider")
		}
		return NewGeminiProvider(cfg.GeminiURL, cfg.GeminiModel, cfg.GeminiAPIKey), nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %q", cfg.LLMProvider)
	}
}

// ProviderInfo describes an available LLM provider.
type ProviderInfo struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}

// ModelVariant defines a named model within a provider family.
type ModelVariant struct {
	Name    string // key used in provider map, e.g. "anthropic-sonnet"
	ModelID string // actual model ID sent to the API
	Label   string // display label for the UI
}

// AnthropicModels lists the Anthropic model variants to register.
var AnthropicModels = []ModelVariant{
	{Name: "anthropic-sonnet", ModelID: "claude-sonnet-4-5-20250929", Label: "Sonnet 4.5"},
	{Name: "anthropic-haiku", ModelID: "claude-haiku-4-5-20251001", Label: "Haiku 4.5"},
}

// OpenAIModels lists the OpenAI model variants to register.
var OpenAIModels = []ModelVariant{
	{Name: "openai-gpt4o", ModelID: "gpt-4o", Label: "GPT-4o"},
	{Name: "openai-gpt4o-mini", ModelID: "gpt-4o-mini", Label: "GPT-4o mini"},
}

// GeminiModels lists the Google Gemini model variants to register.
var GeminiModels = []ModelVariant{
	{Name: "gemini-flash", ModelID: "gemini-2.5-flash-preview-04-17", Label: "Gemini 2.5 Flash"},
	{Name: "gemini-pro", ModelID: "gemini-2.5-pro-preview-05-06", Label: "Gemini 2.5 Pro"},
}

// AvailableProviders returns a list of LLM providers that have valid
// configuration. Ollama is always included. Anthropic and OpenAI register
// multiple model variants when their API keys are set.
func AvailableProviders(cfg *config.Config) []ProviderInfo {
	var providers []ProviderInfo

	// Ollama is always available (runs locally)
	providers = append(providers, ProviderInfo{
		Name:  "ollama",
		Model: cfg.OllamaModel,
	})

	if cfg.AnthropicAPIKey != "" {
		for _, m := range AnthropicModels {
			providers = append(providers, ProviderInfo{
				Name:  m.Name,
				Model: m.Label,
			})
		}
	}

	if cfg.OpenAIAPIKey != "" {
		for _, m := range OpenAIModels {
			providers = append(providers, ProviderInfo{
				Name:  m.Name,
				Model: m.Label,
			})
		}
	}

	if cfg.GeminiAPIKey != "" {
		for _, m := range GeminiModels {
			providers = append(providers, ProviderInfo{
				Name:  m.Name,
				Model: m.Label,
			})
		}
	}

	return providers
}
