package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adham90/opentrace/internal/config"
)

func TestModelRegistry_FallbackWhenNoAPIs(t *testing.T) {
	// With no reachable APIs, should fall back to hardcoded + default ollama
	cfg := &config.Config{
		OllamaURL:       "http://127.0.0.1:1", // unreachable
		OllamaModel:     "llama3.2",
		AnthropicAPIKey:  "sk-ant-test",
		OpenAIAPIKey:     "sk-openai-test",
		OpenAIURL:        "http://127.0.0.1:1", // unreachable
		GeminiAPIKey:     "test-gemini-key",
		GeminiURL:        "http://127.0.0.1:1", // unreachable
	}

	mr := NewModelRegistry(cfg)
	ctx := context.Background()
	models := mr.ListModels(ctx)

	// Should have: 1 ollama fallback + anthropic hardcoded + openai hardcoded + gemini hardcoded
	wantMin := 1 + len(AnthropicModels) + len(OpenAIModels) + len(GeminiModels)
	if len(models) != wantMin {
		t.Errorf("expected %d models, got %d: %v", wantMin, len(models), models)
	}

	// First should be ollama fallback
	if models[0].Name != "ollama" {
		t.Errorf("first model name = %q, want %q", models[0].Name, "ollama")
	}
}

func TestModelRegistry_OllamaAPI(t *testing.T) {
	// Mock Ollama /api/tags
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "llama3.2:latest"},
				{"name": "qwen2.5-coder:7b"},
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		OllamaURL:   ts.URL,
		OllamaModel: "llama3.2",
	}

	mr := NewModelRegistry(cfg)
	models := mr.ListModels(context.Background())

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d: %v", len(models), models)
	}
	if models[0].Name != "ollama:llama3.2:latest" {
		t.Errorf("model[0].Name = %q, want %q", models[0].Name, "ollama:llama3.2:latest")
	}
	if models[1].Name != "ollama:qwen2.5-coder:7b" {
		t.Errorf("model[1].Name = %q, want %q", models[1].Name, "ollama:qwen2.5-coder:7b")
	}
}

func TestModelRegistry_OpenAIAPI(t *testing.T) {
	// Mock OpenAI /v1/models
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		// Verify auth header
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{
				{"id": "gpt-4o"},
				{"id": "gpt-4o-mini"},
				{"id": "o1"},
				{"id": "text-embedding-ada-002"}, // should be filtered out
				{"id": "dall-e-3"},                // should be filtered out
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		OllamaURL:    "http://127.0.0.1:1", // unreachable — will fallback
		OllamaModel:  "llama3.2",
		OpenAIAPIKey: "sk-test",
		OpenAIURL:    ts.URL,
	}

	mr := NewModelRegistry(cfg)
	models := mr.ListModels(context.Background())

	// Should have: 1 ollama fallback + 3 openai chat models (not embedding/dalle)
	openaiCount := 0
	for _, m := range models {
		if m.Name == "openai:gpt-4o" || m.Name == "openai:gpt-4o-mini" || m.Name == "openai:o1" {
			openaiCount++
		}
		// These should NOT be present
		if m.Name == "openai:text-embedding-ada-002" || m.Name == "openai:dall-e-3" {
			t.Errorf("non-chat model %q should be filtered out", m.Name)
		}
	}
	if openaiCount != 3 {
		t.Errorf("expected 3 openai chat models, got %d", openaiCount)
	}
}

func TestModelRegistry_GeminiAPI(t *testing.T) {
	// Mock Gemini /v1beta/models
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{
					"name":                       "models/gemini-2.5-flash-preview-04-17",
					"displayName":                "Gemini 2.5 Flash Preview",
					"supportedGenerationMethods": []string{"generateContent", "countTokens"},
				},
				{
					"name":                       "models/gemini-2.5-pro-preview-05-06",
					"displayName":                "Gemini 2.5 Pro Preview",
					"supportedGenerationMethods": []string{"generateContent"},
				},
				{
					"name":                       "models/text-embedding-004",
					"displayName":                "Text Embedding 004",
					"supportedGenerationMethods": []string{"embedContent"},
				},
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		OllamaURL:    "http://127.0.0.1:1",
		OllamaModel:  "llama3.2",
		GeminiAPIKey: "test-key",
		GeminiURL:    ts.URL,
	}

	mr := NewModelRegistry(cfg)
	models := mr.ListModels(context.Background())

	geminiCount := 0
	for _, m := range models {
		if m.Name == "gemini:gemini-2.5-flash-preview-04-17" || m.Name == "gemini:gemini-2.5-pro-preview-05-06" {
			geminiCount++
		}
		if m.Name == "gemini:text-embedding-004" {
			t.Error("embedding model should be filtered out")
		}
	}
	if geminiCount != 2 {
		t.Errorf("expected 2 gemini chat models, got %d", geminiCount)
	}
}

func TestModelRegistry_Cache(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "llama3.2:latest"},
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		OllamaURL:   ts.URL,
		OllamaModel: "llama3.2",
	}

	mr := NewModelRegistry(cfg)
	ctx := context.Background()

	// First call fetches from API
	models1 := mr.ListModels(ctx)
	// Second call should use cache
	models2 := mr.ListModels(ctx)

	if callCount != 1 {
		t.Errorf("expected 1 API call (cached), got %d", callCount)
	}
	if len(models1) != len(models2) {
		t.Errorf("cached result differs: %d vs %d", len(models1), len(models2))
	}
}

func TestIsOpenAIChatModel(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"gpt-4o", true},
		{"gpt-4o-mini", true},
		{"gpt-4-turbo", true},
		{"gpt-3.5-turbo", true},
		{"o1", true},
		{"o1-mini", true},
		{"o3-mini", true},
		{"chatgpt-4o-latest", true},
		{"text-embedding-ada-002", false},
		{"dall-e-3", false},
		{"whisper-1", false},
		{"tts-1", false},
	}

	for _, tt := range tests {
		got := isOpenAIChatModel(tt.id)
		if got != tt.want {
			t.Errorf("isOpenAIChatModel(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestSupportsGenerateContent(t *testing.T) {
	if !supportsGenerateContent([]string{"generateContent", "countTokens"}) {
		t.Error("expected true for generateContent")
	}
	if supportsGenerateContent([]string{"embedContent"}) {
		t.Error("expected false for embedContent only")
	}
	if supportsGenerateContent(nil) {
		t.Error("expected false for nil")
	}
}
