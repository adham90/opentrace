package vmagent

import (
	"fmt"
	"os"
	"time"
)

// Config holds the agent configuration, loaded from environment variables.
type Config struct {
	ServerURL string
	APIKey    string
	Interval  time.Duration
	Labels    map[string]string
}

// LoadConfig reads agent configuration from environment variables.
func LoadConfig() (*Config, error) {
	serverURL := os.Getenv("OPENTRACE_SERVER_URL")
	if serverURL == "" {
		return nil, fmt.Errorf("OPENTRACE_SERVER_URL is required")
	}

	interval := 30 * time.Second
	if v := os.Getenv("OPENTRACE_AGENT_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid OPENTRACE_AGENT_INTERVAL: %w", err)
		}
		interval = d
	}

	return &Config{
		ServerURL: serverURL,
		APIKey:    os.Getenv("OPENTRACE_API_KEY"),
		Interval:  interval,
	}, nil
}
