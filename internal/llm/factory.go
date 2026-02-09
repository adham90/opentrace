package llm

import (
	"fmt"
	"strings"
	"sync"

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
	{Name: "anthropic-opus", ModelID: "claude-opus-4-20250514", Label: "Opus 4"},
	{Name: "anthropic-sonnet", ModelID: "claude-sonnet-4-5-20250929", Label: "Sonnet 4.5"},
	{Name: "anthropic-haiku", ModelID: "claude-haiku-4-5-20251001", Label: "Haiku 4.5"},
}

// OpenAIModels lists the OpenAI model variants to register.
var OpenAIModels = []ModelVariant{
	{Name: "openai-o3-mini", ModelID: "o3-mini", Label: "o3-mini"},
	{Name: "openai-o1", ModelID: "o1", Label: "o1"},
	{Name: "openai-o1-mini", ModelID: "o1-mini", Label: "o1-mini"},
	{Name: "openai-gpt4o", ModelID: "gpt-4o", Label: "GPT-4o"},
	{Name: "openai-gpt4o-mini", ModelID: "gpt-4o-mini", Label: "GPT-4o mini"},
	{Name: "openai-gpt4-turbo", ModelID: "gpt-4-turbo", Label: "GPT-4 Turbo"},
}

// GeminiModels lists the Google Gemini model variants to register.
var GeminiModels = []ModelVariant{
	{Name: "gemini-flash", ModelID: "gemini-2.5-flash-preview-04-17", Label: "Gemini 2.5 Flash"},
	{Name: "gemini-pro", ModelID: "gemini-2.5-pro-preview-05-06", Label: "Gemini 2.5 Pro"},
	{Name: "gemini-2.0-flash", ModelID: "gemini-2.0-flash", Label: "Gemini 2.0 Flash"},
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

// NewProviderByName creates an LLMProvider for a named model variant.
//
// Supported name formats:
//   - Empty string or "ollama" — returns the default Ollama provider
//   - Hardcoded alias like "anthropic-sonnet", "openai-gpt4o" — looked up in ModelVariant slices
//   - Dynamic "provider:modelID" like "openai:gpt-4o", "gemini:gemini-2.5-pro" — creates
//     a provider for any model ID using the configured API keys/URLs
func NewProviderByName(name string, cfg *config.Config) (LLMProvider, error) {
	if name == "" || name == "ollama" {
		return NewOllamaProvider(cfg.OllamaURL, cfg.OllamaModel), nil
	}

	// Handle "provider:modelID" format for dynamically discovered models
	if parts := strings.SplitN(name, ":", 2); len(parts) == 2 {
		provider, modelID := parts[0], parts[1]
		switch provider {
		case "ollama":
			return NewOllamaProvider(cfg.OllamaURL, modelID), nil
		case "openai":
			if cfg.OpenAIAPIKey == "" {
				return nil, fmt.Errorf("OPENTRACE_OPENAI_API_KEY is required for %s", name)
			}
			return NewOpenAIProvider(cfg.OpenAIURL, modelID, cfg.OpenAIAPIKey), nil
		case "anthropic":
			if cfg.AnthropicAPIKey == "" {
				return nil, fmt.Errorf("OPENTRACE_ANTHROPIC_API_KEY is required for %s", name)
			}
			return NewAnthropicProvider(cfg.AnthropicURL, modelID, cfg.AnthropicAPIKey), nil
		case "gemini":
			if cfg.GeminiAPIKey == "" {
				return nil, fmt.Errorf("OPENTRACE_GEMINI_API_KEY is required for %s", name)
			}
			return NewGeminiProvider(cfg.GeminiURL, modelID, cfg.GeminiAPIKey), nil
		default:
			return nil, fmt.Errorf("unknown provider prefix: %q", provider)
		}
	}

	// Legacy hardcoded alias lookup
	for _, m := range AnthropicModels {
		if m.Name == name {
			if cfg.AnthropicAPIKey == "" {
				return nil, fmt.Errorf("OPENTRACE_ANTHROPIC_API_KEY is required for %s", name)
			}
			return NewAnthropicProvider(cfg.AnthropicURL, m.ModelID, cfg.AnthropicAPIKey), nil
		}
	}

	for _, m := range OpenAIModels {
		if m.Name == name {
			if cfg.OpenAIAPIKey == "" {
				return nil, fmt.Errorf("OPENTRACE_OPENAI_API_KEY is required for %s", name)
			}
			return NewOpenAIProvider(cfg.OpenAIURL, m.ModelID, cfg.OpenAIAPIKey), nil
		}
	}

	for _, m := range GeminiModels {
		if m.Name == name {
			if cfg.GeminiAPIKey == "" {
				return nil, fmt.Errorf("OPENTRACE_GEMINI_API_KEY is required for %s", name)
			}
			return NewGeminiProvider(cfg.GeminiURL, m.ModelID, cfg.GeminiAPIKey), nil
		}
	}

	return nil, fmt.Errorf("unknown model variant: %q", name)
}

// ProviderCache is a thread-safe lazy cache for LLM providers.
// It returns the global default for empty model names, and creates
// providers on first use for named variants.
type ProviderCache struct {
	mu         sync.RWMutex
	cfg        *config.Config
	defaultLLM LLMProvider
	cache      map[string]LLMProvider
}

// NewProviderCache creates a new ProviderCache.
func NewProviderCache(cfg *config.Config, defaultLLM LLMProvider) *ProviderCache {
	return &ProviderCache{
		cfg:        cfg,
		defaultLLM: defaultLLM,
		cache:      make(map[string]LLMProvider),
	}
}

// Get returns the LLMProvider for the given model name.
// Empty string returns the global default. Named variants are created
// on first access and cached for reuse.
func (pc *ProviderCache) Get(modelName string) (LLMProvider, error) {
	if modelName == "" {
		return pc.defaultLLM, nil
	}

	// Fast path: check read lock
	pc.mu.RLock()
	if p, ok := pc.cache[modelName]; ok {
		pc.mu.RUnlock()
		return p, nil
	}
	pc.mu.RUnlock()

	// Slow path: create and cache
	pc.mu.Lock()
	defer pc.mu.Unlock()

	// Double-check after acquiring write lock
	if p, ok := pc.cache[modelName]; ok {
		return p, nil
	}

	p, err := NewProviderByName(modelName, pc.cfg)
	if err != nil {
		return nil, err
	}

	pc.cache[modelName] = p
	return p, nil
}
