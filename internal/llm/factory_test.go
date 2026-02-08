package llm

import (
	"testing"

	"github.com/opentrace/opentrace/internal/config"
)

func TestNewLLMProvider_Ollama(t *testing.T) {
	cfg := &config.Config{
		LLMProvider:        "ollama",
		OllamaURL:          "http://localhost:11434",
		OllamaModel:        "llama3.2",
		EmbeddingModel:     "nomic-embed-text",
		EmbeddingDimension: 768,
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

func TestNewLLMProvider_Unknown(t *testing.T) {
	cfg := &config.Config{
		LLMProvider: "gpt-magic",
	}

	_, err := NewLLMProvider(cfg)
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}

func TestNewEmbeddingProvider_Ollama(t *testing.T) {
	cfg := &config.Config{
		EmbeddingProvider:  "ollama",
		OllamaURL:          "http://localhost:11434",
		OllamaModel:        "llama3.2",
		EmbeddingModel:     "nomic-embed-text",
		EmbeddingDimension: 768,
	}

	p, err := NewEmbeddingProvider(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ollama, ok := p.(*OllamaProvider)
	if !ok {
		t.Fatalf("expected *OllamaProvider, got %T", p)
	}
	if ollama.embeddingModel != "nomic-embed-text" {
		t.Errorf("got embeddingModel %q, want %q", ollama.embeddingModel, "nomic-embed-text")
	}
	if ollama.Dimension() != 768 {
		t.Errorf("got dimension %d, want 768", ollama.Dimension())
	}
}

func TestNewEmbeddingProvider_Unknown(t *testing.T) {
	cfg := &config.Config{
		EmbeddingProvider: "deepmind",
	}

	_, err := NewEmbeddingProvider(cfg)
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}
