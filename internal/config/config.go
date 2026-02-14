package config

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	DataDir           string
	LLMProvider       string
	OllamaURL         string
	OllamaModel       string
	ListenAddr         string
	APIKey             string

	AnthropicAPIKey string
	AnthropicModel  string
	AnthropicURL    string
	OpenAIAPIKey    string
	OpenAIModel     string
	OpenAIURL       string
	GeminiAPIKey    string
	GeminiModel     string
	GeminiURL       string

	MaxQueryRows        int
	StatementTimeoutMS  int
	MaxAgentSteps       int
	MaxToolCalls        int
	MaxObservationBytes int

	TrustedProxies []string

	TLSCert string
	TLSKey  string

	DevMode bool
}

// LoadEnvFile reads a .env file and sets any variables not already in the environment.
// Missing file is not an error. Warns if the file has world-readable permissions.
func LoadEnvFile(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return // File doesn't exist — that's fine
	}

	// Warn if .env is readable by group or others (potential credential leak)
	if perm := info.Mode().Perm(); perm&0o044 != 0 {
		slog.Warn(".env file has insecure permissions — should be 0600",
			"path", path,
			"permissions", fmt.Sprintf("%04o", perm))
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// Don't override existing env vars
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

func Load() (*Config, error) {
	dataDir := os.Getenv("OPENTRACE_DATA_DIR")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("determining home directory: %w", err)
		}
		dataDir = filepath.Join(home, ".opentrace")
	}

	maxQueryRows, err := envOrDefaultInt("OPENTRACE_MAX_QUERY_ROWS", 500)
	if err != nil {
		return nil, err
	}

	stmtTimeout, err := envOrDefaultInt("OPENTRACE_STATEMENT_TIMEOUT_MS", 5000)
	if err != nil {
		return nil, err
	}

	maxSteps, err := envOrDefaultInt("OPENTRACE_MAX_AGENT_STEPS", 12)
	if err != nil {
		return nil, err
	}

	maxTools, err := envOrDefaultInt("OPENTRACE_MAX_TOOL_CALLS", 8)
	if err != nil {
		return nil, err
	}

	maxObs, err := envOrDefaultInt("OPENTRACE_MAX_OBSERVATION_BYTES", 8192)
	if err != nil {
		return nil, err
	}

	return &Config{
		DataDir:             dataDir,
		LLMProvider:         envOrDefault("OPENTRACE_LLM_PROVIDER", "ollama"),
		OllamaURL:           envOrDefault("OPENTRACE_OLLAMA_URL", "http://localhost:11434"),
		OllamaModel:         envOrDefault("OPENTRACE_OLLAMA_MODEL", "llama3.2"),
		ListenAddr:          envOrDefault("OPENTRACE_LISTEN_ADDR", "127.0.0.1:8080"),
		APIKey:              os.Getenv("OPENTRACE_API_KEY"),
		AnthropicAPIKey:     os.Getenv("OPENTRACE_ANTHROPIC_API_KEY"),
		AnthropicModel:      envOrDefault("OPENTRACE_ANTHROPIC_MODEL", "claude-sonnet-4-5-20250929"),
		AnthropicURL:        envOrDefault("OPENTRACE_ANTHROPIC_URL", "https://api.anthropic.com"),
		OpenAIAPIKey:        os.Getenv("OPENTRACE_OPENAI_API_KEY"),
		OpenAIModel:         envOrDefault("OPENTRACE_OPENAI_MODEL", "gpt-4.1-mini"),
		OpenAIURL:           envOrDefault("OPENTRACE_OPENAI_URL", "https://api.openai.com"),
		GeminiAPIKey:        os.Getenv("OPENTRACE_GEMINI_API_KEY"),
		GeminiModel:         envOrDefault("OPENTRACE_GEMINI_MODEL", "gemini-2.5-flash"),
		GeminiURL:           envOrDefault("OPENTRACE_GEMINI_URL", "https://generativelanguage.googleapis.com"),
		MaxQueryRows:        maxQueryRows,
		StatementTimeoutMS:  stmtTimeout,
		MaxAgentSteps:       maxSteps,
		MaxToolCalls:        maxTools,
		MaxObservationBytes: maxObs,
		TrustedProxies:      parseTrustedProxies(os.Getenv("OPENTRACE_TRUSTED_PROXIES")),
		TLSCert:             os.Getenv("OPENTRACE_TLS_CERT"),
		TLSKey:              os.Getenv("OPENTRACE_TLS_KEY"),
		DevMode:             os.Getenv("OPENTRACE_DEV") == "true",
	}, nil
}

// DatabasePath returns the full path to the SQLite database file.
func (c *Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "opentrace.db")
}

// LLMOverrides holds LLM settings from the database that override env-var defaults.
// Empty fields are ignored (env-var value is kept).
type LLMOverrides struct {
	DefaultProvider string
	AnthropicAPIKey string
	AnthropicURL    string
	OpenAIAPIKey    string
	OpenAIURL       string
	GeminiAPIKey    string
	GeminiURL       string
	OllamaURL       string
	OllamaModel     string
}

// ApplyLLMOverrides merges database LLM settings over environment defaults.
// Non-empty override values take precedence; empty fields keep the env-var value.
func (c *Config) ApplyLLMOverrides(o LLMOverrides) {
	if o.DefaultProvider != "" {
		c.LLMProvider = o.DefaultProvider
	}
	if o.AnthropicAPIKey != "" {
		c.AnthropicAPIKey = o.AnthropicAPIKey
	}
	if o.AnthropicURL != "" {
		c.AnthropicURL = o.AnthropicURL
	}
	if o.OpenAIAPIKey != "" {
		c.OpenAIAPIKey = o.OpenAIAPIKey
	}
	if o.OpenAIURL != "" {
		c.OpenAIURL = o.OpenAIURL
	}
	if o.GeminiAPIKey != "" {
		c.GeminiAPIKey = o.GeminiAPIKey
	}
	if o.GeminiURL != "" {
		c.GeminiURL = o.GeminiURL
	}
	if o.OllamaURL != "" {
		c.OllamaURL = o.OllamaURL
	}
	if o.OllamaModel != "" {
		c.OllamaModel = o.OllamaModel
	}
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func parseTrustedProxies(val string) []string {
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func envOrDefaultInt(key string, defaultVal int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid value for %s: %q (must be an integer)", key, v)
	}
	return n, nil
}
