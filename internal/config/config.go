package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AppDatabaseURL    string
	LLMProvider       string
	OllamaURL         string
	OllamaModel       string
	EmbeddingProvider  string
	EmbeddingModel     string
	EmbeddingDimension int
	ListenAddr         string
	APIKey             string

	MaxQueryRows        int
	StatementTimeoutMS  int
	MaxAgentSteps       int
	MaxToolCalls        int
	MaxObservationBytes int
}

// LoadEnvFile reads a .env file and sets any variables not already in the environment.
// Missing file is not an error.
func LoadEnvFile(path string) {
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
	dbURL := os.Getenv("OPENTRACE_APP_DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("OPENTRACE_APP_DATABASE_URL is required")
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

	embDim, err := envOrDefaultInt("OPENTRACE_EMBEDDING_DIMENSION", 768)
	if err != nil {
		return nil, err
	}

	return &Config{
		AppDatabaseURL:      dbURL,
		LLMProvider:         envOrDefault("OPENTRACE_LLM_PROVIDER", "ollama"),
		OllamaURL:           envOrDefault("OPENTRACE_OLLAMA_URL", "http://localhost:11434"),
		OllamaModel:         envOrDefault("OPENTRACE_OLLAMA_MODEL", "llama3.2"),
		EmbeddingProvider:   envOrDefault("OPENTRACE_EMBEDDING_PROVIDER", "ollama"),
		EmbeddingModel:      envOrDefault("OPENTRACE_EMBEDDING_MODEL", "nomic-embed-text"),
		EmbeddingDimension:  embDim,
		ListenAddr:          envOrDefault("OPENTRACE_LISTEN_ADDR", ":8080"),
		APIKey:              os.Getenv("OPENTRACE_API_KEY"),
		MaxQueryRows:        maxQueryRows,
		StatementTimeoutMS:  stmtTimeout,
		MaxAgentSteps:       maxSteps,
		MaxToolCalls:        maxTools,
		MaxObservationBytes: maxObs,
	}, nil
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
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
