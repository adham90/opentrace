package llm

import (
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/config"
)

func TestNewLLMProvider_Ollama(t *testing.T) {
	cfg := &config.Config{
		LLMProvider: "ollama",
		OllamaURL:   "http://localhost:11434",
		OllamaModel: "llama3.2",
	}

	p, err := NewLLMProvider(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ollama, ok := p.(*OllamaProvider)
	if !ok {
		t.Fatalf("expected *OllamaProvider, got %T", p)
	}
	if ollama.model != "llama3.2" {
		t.Errorf("got model %q, want %q", ollama.model, "llama3.2")
	}
	if ollama.baseURL != "http://localhost:11434" {
		t.Errorf("got baseURL %q, want %q", ollama.baseURL, "http://localhost:11434")
	}
}

func TestNewLLMProvider_Anthropic(t *testing.T) {
	cfg := &config.Config{
		LLMProvider:     "anthropic",
		AnthropicAPIKey: "sk-ant-test",
		AnthropicModel:  "claude-sonnet-4-20250514",
		AnthropicURL:    "https://api.anthropic.com",
	}

	p, err := NewLLMProvider(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ap, ok := p.(*AnthropicProvider)
	if !ok {
		t.Fatalf("expected *AnthropicProvider, got %T", p)
	}
	if ap.model != "claude-sonnet-4-20250514" {
		t.Errorf("got model %q, want %q", ap.model, "claude-sonnet-4-20250514")
	}
	if ap.apiKey != "sk-ant-test" {
		t.Errorf("got apiKey %q, want %q", ap.apiKey, "sk-ant-test")
	}
}

func TestNewLLMProvider_Anthropic_MissingKey(t *testing.T) {
	cfg := &config.Config{
		LLMProvider: "anthropic",
		// No API key
	}

	_, err := NewLLMProvider(cfg)
	if err == nil {
		t.Fatal("expected error for missing API key, got nil")
	}
}

func TestNewLLMProvider_OpenAI(t *testing.T) {
	cfg := &config.Config{
		LLMProvider:  "openai",
		OpenAIAPIKey: "sk-openai-test",
		OpenAIModel:  "gpt-4o",
		OpenAIURL:    "https://api.openai.com",
	}

	p, err := NewLLMProvider(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("expected *OpenAIProvider, got %T", p)
	}
	if op.model != "gpt-4o" {
		t.Errorf("got model %q, want %q", op.model, "gpt-4o")
	}
	if op.apiKey != "sk-openai-test" {
		t.Errorf("got apiKey %q, want %q", op.apiKey, "sk-openai-test")
	}
}

func TestNewLLMProvider_OpenAI_MissingKey(t *testing.T) {
	cfg := &config.Config{
		LLMProvider: "openai",
		// No API key
	}

	_, err := NewLLMProvider(cfg)
	if err == nil {
		t.Fatal("expected error for missing API key, got nil")
	}
}

func TestNewLLMProvider_Gemini(t *testing.T) {
	cfg := &config.Config{
		LLMProvider:  "gemini",
		GeminiAPIKey: "test-gemini-key",
		GeminiModel:  "gemini-2.5-flash-preview-04-17",
		GeminiURL:    "https://generativelanguage.googleapis.com",
	}

	p, err := NewLLMProvider(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gp, ok := p.(*GeminiProvider)
	if !ok {
		t.Fatalf("expected *GeminiProvider, got %T", p)
	}
	if gp.model != "gemini-2.5-flash-preview-04-17" {
		t.Errorf("got model %q, want %q", gp.model, "gemini-2.5-flash-preview-04-17")
	}
	if gp.apiKey != "test-gemini-key" {
		t.Errorf("got apiKey %q, want %q", gp.apiKey, "test-gemini-key")
	}
	if gp.baseURL != "https://generativelanguage.googleapis.com" {
		t.Errorf("got baseURL %q, want %q", gp.baseURL, "https://generativelanguage.googleapis.com")
	}
}

func TestNewLLMProvider_Gemini_CustomURL(t *testing.T) {
	cfg := &config.Config{
		LLMProvider:  "gemini",
		GeminiAPIKey: "test-key",
		GeminiModel:  "gemini-2.5-pro-preview-05-06",
		GeminiURL:    "https://custom-proxy.example.com",
	}

	p, err := NewLLMProvider(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gp := p.(*GeminiProvider)
	if gp.baseURL != "https://custom-proxy.example.com" {
		t.Errorf("baseURL = %q, want custom proxy URL", gp.baseURL)
	}
	if gp.model != "gemini-2.5-pro-preview-05-06" {
		t.Errorf("model = %q, want pro model", gp.model)
	}
}

func TestNewLLMProvider_Gemini_MissingKey(t *testing.T) {
	cfg := &config.Config{
		LLMProvider: "gemini",
		// No API key
	}

	_, err := NewLLMProvider(cfg)
	if err == nil {
		t.Fatal("expected error for missing API key, got nil")
	}
	if !strings.Contains(err.Error(), "OPENTRACE_GEMINI_API_KEY") {
		t.Errorf("error should mention the env var name, got %q", err.Error())
	}
}

func TestNewLLMProvider_Unknown(t *testing.T) {
	cfg := &config.Config{
		LLMProvider: "gpt-magic",
	}

	_, err := NewLLMProvider(cfg)
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}

func TestAvailableProviders_OllamaOnly(t *testing.T) {
	cfg := &config.Config{
		OllamaModel: "llama3.2",
	}

	providers := AvailableProviders(cfg)
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if providers[0].Name != "ollama" {
		t.Errorf("expected ollama, got %q", providers[0].Name)
	}
	if providers[0].Model != "llama3.2" {
		t.Errorf("expected llama3.2, got %q", providers[0].Model)
	}
}

func TestAvailableProviders_AllProviders(t *testing.T) {
	cfg := &config.Config{
		OllamaModel:     "llama3.2",
		AnthropicAPIKey:  "sk-ant-test",
		OpenAIAPIKey:     "sk-openai-test",
		GeminiAPIKey:     "test-gemini-key",
	}

	providers := AvailableProviders(cfg)
	// 1 ollama + 2 anthropic + 2 openai + 2 gemini = 7
	wantCount := 1 + len(AnthropicModels) + len(OpenAIModels) + len(GeminiModels)
	if len(providers) != wantCount {
		t.Fatalf("expected %d providers, got %d: %v", wantCount, len(providers), providers)
	}

	names := make(map[string]bool)
	for _, p := range providers {
		names[p.Name] = true
	}
	for _, want := range []string{"ollama", "anthropic-sonnet", "anthropic-haiku", "openai-gpt4o", "openai-gpt4o-mini", "gemini-flash", "gemini-pro"} {
		if !names[want] {
			t.Errorf("expected provider %q in list", want)
		}
	}
}

func TestAvailableProviders_GeminiOnly(t *testing.T) {
	cfg := &config.Config{
		OllamaModel:  "llama3.2",
		GeminiAPIKey: "test-gemini-key",
	}

	providers := AvailableProviders(cfg)
	wantCount := 1 + len(GeminiModels)
	if len(providers) != wantCount {
		t.Fatalf("expected %d providers, got %d", wantCount, len(providers))
	}

	names := make(map[string]bool)
	for _, p := range providers {
		names[p.Name] = true
	}
	if !names["ollama"] {
		t.Error("expected ollama provider")
	}
	for _, m := range GeminiModels {
		if !names[m.Name] {
			t.Errorf("expected provider %q in list", m.Name)
		}
	}
}

func TestAvailableProviders_AnthropicOnly(t *testing.T) {
	cfg := &config.Config{
		OllamaModel:     "llama3.2",
		AnthropicAPIKey:  "sk-ant-test",
	}

	providers := AvailableProviders(cfg)
	wantCount := 1 + len(AnthropicModels)
	if len(providers) != wantCount {
		t.Fatalf("expected %d providers, got %d", wantCount, len(providers))
	}
}
