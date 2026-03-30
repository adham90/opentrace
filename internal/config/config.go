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
	// DataDir is the directory for SQLite database and backups. Defaults to ~/.opentrace.
	DataDir string
	// ListenAddr is the address the HTTP server binds to. Used by 'serve' command.
	ListenAddr string
	// APIKey is the pre-shared API key for authenticating API requests.
	APIKey string

	// MaxQueryRows is the maximum rows returned by database queries. Default 500.
	MaxQueryRows int
	// StatementTimeoutMS is the SQLite statement timeout in milliseconds. Default 5000.
	StatementTimeoutMS int

	// TrustedProxies lists IP addresses trusted to set X-Forwarded-For headers.
	TrustedProxies []string

	// CORSAllowedOrigins lists origins allowed for CORS requests.
	CORSAllowedOrigins []string

	// DevMode enables development mode (verbose logging, relaxed security).
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

	return &Config{
		DataDir:            dataDir,
		ListenAddr:         envOrDefault("OPENTRACE_LISTEN_ADDR", "127.0.0.1:8080"),
		APIKey:             os.Getenv("OPENTRACE_API_KEY"),
		MaxQueryRows:       maxQueryRows,
		StatementTimeoutMS: stmtTimeout,
		TrustedProxies:     parseCommaSeparated(os.Getenv("OPENTRACE_TRUSTED_PROXIES")),
		CORSAllowedOrigins: parseCommaSeparated(os.Getenv("OPENTRACE_CORS_ORIGINS")),
		DevMode:            os.Getenv("OPENTRACE_DEV") == "true",
	}, nil
}

// DatabasePath returns the full path to the SQLite database file.
func (c *Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "opentrace.db")
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// parseCommaSeparated splits a comma-delimited string into trimmed, non-empty parts.
func parseCommaSeparated(val string) []string {
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
