package config

import (
	"os"
	"testing"
)

func clearEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"OPENTRACE_APP_DATABASE_URL",
		"OPENTRACE_LLM_PROVIDER",
		"OPENTRACE_OLLAMA_URL",
		"OPENTRACE_EMBEDDING_PROVIDER",
		"OPENTRACE_EMBEDDING_MODEL",
		"OPENTRACE_LISTEN_ADDR",
		"OPENTRACE_MAX_QUERY_ROWS",
		"OPENTRACE_STATEMENT_TIMEOUT_MS",
		"OPENTRACE_MAX_AGENT_STEPS",
		"OPENTRACE_MAX_TOOL_CALLS",
		"OPENTRACE_MAX_OBSERVATION_BYTES",
		"OPENTRACE_ANTHROPIC_API_KEY",
		"OPENTRACE_ANTHROPIC_MODEL",
		"OPENTRACE_ANTHROPIC_URL",
		"OPENTRACE_OPENAI_API_KEY",
		"OPENTRACE_OPENAI_MODEL",
		"OPENTRACE_OPENAI_URL",
	}
	for _, v := range envVars {
		os.Unsetenv(v)
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)
	os.Setenv("OPENTRACE_APP_DATABASE_URL", "postgres://localhost/test")
	defer os.Unsetenv("OPENTRACE_APP_DATABASE_URL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AppDatabaseURL != "postgres://localhost/test" {
		t.Errorf("AppDatabaseURL = %q, want %q", cfg.AppDatabaseURL, "postgres://localhost/test")
	}
	if cfg.LLMProvider != "ollama" {
		t.Errorf("LLMProvider = %q, want %q", cfg.LLMProvider, "ollama")
	}
	if cfg.OllamaURL != "http://localhost:11434" {
		t.Errorf("OllamaURL = %q, want %q", cfg.OllamaURL, "http://localhost:11434")
	}
	if cfg.EmbeddingProvider != "ollama" {
		t.Errorf("EmbeddingProvider = %q, want %q", cfg.EmbeddingProvider, "ollama")
	}
	if cfg.EmbeddingModel != "nomic-embed-text" {
		t.Errorf("EmbeddingModel = %q, want %q", cfg.EmbeddingModel, "nomic-embed-text")
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":8080")
	}
	if cfg.MaxQueryRows != 500 {
		t.Errorf("MaxQueryRows = %d, want %d", cfg.MaxQueryRows, 500)
	}
	if cfg.StatementTimeoutMS != 5000 {
		t.Errorf("StatementTimeoutMS = %d, want %d", cfg.StatementTimeoutMS, 5000)
	}
	if cfg.MaxAgentSteps != 12 {
		t.Errorf("MaxAgentSteps = %d, want %d", cfg.MaxAgentSteps, 12)
	}
	if cfg.MaxToolCalls != 8 {
		t.Errorf("MaxToolCalls = %d, want %d", cfg.MaxToolCalls, 8)
	}
	if cfg.MaxObservationBytes != 8192 {
		t.Errorf("MaxObservationBytes = %d, want %d", cfg.MaxObservationBytes, 8192)
	}
	if cfg.AnthropicAPIKey != "" {
		t.Errorf("AnthropicAPIKey = %q, want empty", cfg.AnthropicAPIKey)
	}
	if cfg.AnthropicModel != "claude-sonnet-4-5-20250929" {
		t.Errorf("AnthropicModel = %q, want %q", cfg.AnthropicModel, "claude-sonnet-4-5-20250929")
	}
	if cfg.AnthropicURL != "https://api.anthropic.com" {
		t.Errorf("AnthropicURL = %q, want %q", cfg.AnthropicURL, "https://api.anthropic.com")
	}
	if cfg.OpenAIAPIKey != "" {
		t.Errorf("OpenAIAPIKey = %q, want empty", cfg.OpenAIAPIKey)
	}
	if cfg.OpenAIModel != "gpt-4o" {
		t.Errorf("OpenAIModel = %q, want %q", cfg.OpenAIModel, "gpt-4o")
	}
	if cfg.OpenAIURL != "https://api.openai.com" {
		t.Errorf("OpenAIURL = %q, want %q", cfg.OpenAIURL, "https://api.openai.com")
	}
}

func TestLoad_AllOverrides(t *testing.T) {
	clearEnv(t)
	overrides := map[string]string{
		"OPENTRACE_APP_DATABASE_URL":      "postgres://prod/opentrace",
		"OPENTRACE_LLM_PROVIDER":          "anthropic",
		"OPENTRACE_OLLAMA_URL":            "http://gpu-server:11434",
		"OPENTRACE_EMBEDDING_PROVIDER":    "openai",
		"OPENTRACE_EMBEDDING_MODEL":       "text-embedding-3-small",
		"OPENTRACE_LISTEN_ADDR":           ":9090",
		"OPENTRACE_MAX_QUERY_ROWS":        "1000",
		"OPENTRACE_STATEMENT_TIMEOUT_MS":  "10000",
		"OPENTRACE_MAX_AGENT_STEPS":       "20",
		"OPENTRACE_MAX_TOOL_CALLS":        "15",
		"OPENTRACE_MAX_OBSERVATION_BYTES": "16384",
		"OPENTRACE_ANTHROPIC_API_KEY":     "sk-ant-test",
		"OPENTRACE_ANTHROPIC_MODEL":       "claude-opus-4-20250514",
		"OPENTRACE_ANTHROPIC_URL":         "https://custom-anthropic.example.com",
		"OPENTRACE_OPENAI_API_KEY":        "sk-openai-test",
		"OPENTRACE_OPENAI_MODEL":          "gpt-4-turbo",
		"OPENTRACE_OPENAI_URL":            "https://custom-openai.example.com",
	}
	for k, v := range overrides {
		os.Setenv(k, v)
		defer os.Unsetenv(k)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AppDatabaseURL != "postgres://prod/opentrace" {
		t.Errorf("AppDatabaseURL = %q", cfg.AppDatabaseURL)
	}
	if cfg.LLMProvider != "anthropic" {
		t.Errorf("LLMProvider = %q", cfg.LLMProvider)
	}
	if cfg.OllamaURL != "http://gpu-server:11434" {
		t.Errorf("OllamaURL = %q", cfg.OllamaURL)
	}
	if cfg.EmbeddingProvider != "openai" {
		t.Errorf("EmbeddingProvider = %q", cfg.EmbeddingProvider)
	}
	if cfg.EmbeddingModel != "text-embedding-3-small" {
		t.Errorf("EmbeddingModel = %q", cfg.EmbeddingModel)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.MaxQueryRows != 1000 {
		t.Errorf("MaxQueryRows = %d", cfg.MaxQueryRows)
	}
	if cfg.StatementTimeoutMS != 10000 {
		t.Errorf("StatementTimeoutMS = %d", cfg.StatementTimeoutMS)
	}
	if cfg.MaxAgentSteps != 20 {
		t.Errorf("MaxAgentSteps = %d", cfg.MaxAgentSteps)
	}
	if cfg.MaxToolCalls != 15 {
		t.Errorf("MaxToolCalls = %d", cfg.MaxToolCalls)
	}
	if cfg.MaxObservationBytes != 16384 {
		t.Errorf("MaxObservationBytes = %d", cfg.MaxObservationBytes)
	}
	if cfg.AnthropicAPIKey != "sk-ant-test" {
		t.Errorf("AnthropicAPIKey = %q", cfg.AnthropicAPIKey)
	}
	if cfg.AnthropicModel != "claude-opus-4-20250514" {
		t.Errorf("AnthropicModel = %q", cfg.AnthropicModel)
	}
	if cfg.AnthropicURL != "https://custom-anthropic.example.com" {
		t.Errorf("AnthropicURL = %q", cfg.AnthropicURL)
	}
	if cfg.OpenAIAPIKey != "sk-openai-test" {
		t.Errorf("OpenAIAPIKey = %q", cfg.OpenAIAPIKey)
	}
	if cfg.OpenAIModel != "gpt-4-turbo" {
		t.Errorf("OpenAIModel = %q", cfg.OpenAIModel)
	}
	if cfg.OpenAIURL != "https://custom-openai.example.com" {
		t.Errorf("OpenAIURL = %q", cfg.OpenAIURL)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	clearEnv(t)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing OPENTRACE_APP_DATABASE_URL")
	}
}

func TestLoad_InvalidIntValue(t *testing.T) {
	clearEnv(t)
	os.Setenv("OPENTRACE_APP_DATABASE_URL", "postgres://localhost/test")
	defer os.Unsetenv("OPENTRACE_APP_DATABASE_URL")
	os.Setenv("OPENTRACE_MAX_QUERY_ROWS", "not-a-number")
	defer os.Unsetenv("OPENTRACE_MAX_QUERY_ROWS")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid int value")
	}
}
