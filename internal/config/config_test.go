package config

import (
	"os"
	"testing"
)

func clearEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"OPENTRACE_DATA_DIR",
		"OPENTRACE_LLM_PROVIDER",
		"OPENTRACE_OLLAMA_URL",
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
		"OPENTRACE_GEMINI_API_KEY",
		"OPENTRACE_GEMINI_MODEL",
		"OPENTRACE_GEMINI_URL",
	}
	for _, v := range envVars {
		os.Unsetenv(v)
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DataDir == "" {
		t.Error("DataDir should default to ~/.opentrace")
	}
	if cfg.LLMProvider != "ollama" {
		t.Errorf("LLMProvider = %q, want %q", cfg.LLMProvider, "ollama")
	}
	if cfg.OllamaURL != "http://localhost:11434" {
		t.Errorf("OllamaURL = %q, want %q", cfg.OllamaURL, "http://localhost:11434")
	}
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "127.0.0.1:8080")
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
	if cfg.OpenAIModel != "gpt-4.1-mini" {
		t.Errorf("OpenAIModel = %q, want %q", cfg.OpenAIModel, "gpt-4.1-mini")
	}
	if cfg.OpenAIURL != "https://api.openai.com" {
		t.Errorf("OpenAIURL = %q, want %q", cfg.OpenAIURL, "https://api.openai.com")
	}
	if cfg.GeminiAPIKey != "" {
		t.Errorf("GeminiAPIKey = %q, want empty", cfg.GeminiAPIKey)
	}
	if cfg.GeminiModel != "gemini-2.5-flash" {
		t.Errorf("GeminiModel = %q, want %q", cfg.GeminiModel, "gemini-2.5-flash")
	}
	if cfg.GeminiURL != "https://generativelanguage.googleapis.com" {
		t.Errorf("GeminiURL = %q, want %q", cfg.GeminiURL, "https://generativelanguage.googleapis.com")
	}
}

func TestLoad_AllOverrides(t *testing.T) {
	clearEnv(t)
	overrides := map[string]string{
		"OPENTRACE_DATA_DIR":              "/tmp/opentrace-test",
		"OPENTRACE_LLM_PROVIDER":          "anthropic",
		"OPENTRACE_OLLAMA_URL":            "http://gpu-server:11434",
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
		"OPENTRACE_GEMINI_API_KEY":        "AIzaSy-test-key",
		"OPENTRACE_GEMINI_MODEL":          "gemini-2.5-pro-preview-05-06",
		"OPENTRACE_GEMINI_URL":            "https://custom-gemini.example.com",
	}
	for k, v := range overrides {
		os.Setenv(k, v)
		defer os.Unsetenv(k)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DataDir != "/tmp/opentrace-test" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "/tmp/opentrace-test")
	}
	if cfg.LLMProvider != "anthropic" {
		t.Errorf("LLMProvider = %q", cfg.LLMProvider)
	}
	if cfg.OllamaURL != "http://gpu-server:11434" {
		t.Errorf("OllamaURL = %q", cfg.OllamaURL)
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
	if cfg.GeminiAPIKey != "AIzaSy-test-key" {
		t.Errorf("GeminiAPIKey = %q", cfg.GeminiAPIKey)
	}
	if cfg.GeminiModel != "gemini-2.5-pro-preview-05-06" {
		t.Errorf("GeminiModel = %q", cfg.GeminiModel)
	}
	if cfg.GeminiURL != "https://custom-gemini.example.com" {
		t.Errorf("GeminiURL = %q", cfg.GeminiURL)
	}
}

func TestLoad_DataDir_Default(t *testing.T) {
	clearEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should default to $HOME/.opentrace
	home, _ := os.UserHomeDir()
	want := home + "/.opentrace"
	if cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
	if cfg.DatabasePath() != want+"/opentrace.db" {
		t.Errorf("DatabasePath = %q, want %q", cfg.DatabasePath(), want+"/opentrace.db")
	}
}

func TestLoad_InvalidIntValue(t *testing.T) {
	clearEnv(t)
	os.Setenv("OPENTRACE_MAX_QUERY_ROWS", "not-a-number")
	defer os.Unsetenv("OPENTRACE_MAX_QUERY_ROWS")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid int value")
	}
}
