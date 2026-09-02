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

	// CriticalPaths marks the money path: routes, services or error text that
	// pay the bills (checkout, billing, signup). Anything matching one of these
	// substrings sorts to the top of catch-up and error rankings, so a $0 day
	// is never buried under a noisy debug endpoint. Set via
	// OPENTRACE_CRITICAL_PATHS as a comma-separated list; unset means every
	// path ranks the same.
	CriticalPaths []string

	// CORSAllowedOrigins lists origins allowed for CORS requests.
	CORSAllowedOrigins []string

	// DevMode enables development mode (verbose logging, relaxed security).
	DevMode bool

	// SocketPath is the path for a Unix domain socket listener.
	// When set, the server accepts log ingestion over a length-prefixed
	// binary protocol, bypassing HTTP overhead for local apps.
	// Empty string (default) disables the listener.
	SocketPath string

	// DefaultEnv is the environment ingest stamps onto payloads that don't
	// carry an "env" field. Set via OPENTRACE_DEFAULT_ENV; defaults to
	// "production".
	//
	// It does NOT influence the schema migration: the historical backfill in
	// migrations/000001_init.up.sql is static SQL that rewrites
	// environment='' rows to 'production' regardless of this setting. An
	// operator running with OPENTRACE_DEFAULT_ENV set to anything else must
	// rerun those UPDATEs by hand, or history and new rows land in different
	// environments.
	DefaultEnv string

	// Performance contains the process-wide resource budgets selected by
	// OPENTRACE_RESOURCE_PROFILE and any explicit per-budget overrides.
	Performance PerformanceConfig
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
		val = parseEnvValue(val)
		// Don't override existing env vars
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

// parseEnvValue normalizes the right-hand side of a .env assignment the way
// conventional dotenv loaders do: surrounding quotes are removed rather than
// stored verbatim (a quoted secret must not become a different secret), and an
// unquoted value's trailing " # comment" is dropped. Inside double quotes the
// usual \n / \t / \" / \\ escapes are expanded; single quotes are literal.
func parseEnvValue(raw string) string {
	val := strings.TrimSpace(raw)
	if val == "" {
		return ""
	}

	if q := val[0]; q == '"' || q == '\'' {
		if end := closingQuote(val, rune(q)); end > 0 {
			inner := val[1:end]
			if q == '"' {
				return expandEscapes(inner)
			}
			return inner
		}
		// Unterminated quote: treat the value as literal text.
		return val
	}

	// Unquoted: an inline comment must be preceded by whitespace so that
	// values legitimately containing '#' (e.g. a URL fragment) survive.
	for i := 0; i < len(val); i++ {
		if val[i] == '#' && i > 0 && (val[i-1] == ' ' || val[i-1] == '\t') {
			return strings.TrimSpace(val[:i])
		}
	}
	return val
}

// closingQuote returns the index of the quote that terminates a value opened
// with q at index 0, or -1 if there is none. Backslash-escaped quotes inside a
// double-quoted value do not terminate it.
func closingQuote(val string, q rune) int {
	for i := 1; i < len(val); i++ {
		if q == '"' && val[i] == '\\' {
			i++ // skip the escaped character
			continue
		}
		if rune(val[i]) == q {
			return i
		}
	}
	return -1
}

// expandEscapes expands the escape sequences allowed inside a double-quoted
// .env value. Unknown sequences keep their backslash.
func expandEscapes(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '"', '\\', '\'':
			b.WriteByte(s[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
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

	devMode := os.Getenv("OPENTRACE_DEV") == "true"
	performance, err := loadPerformanceConfig(devMode)
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
		CriticalPaths:      parseCommaSeparated(os.Getenv("OPENTRACE_CRITICAL_PATHS")),
		CORSAllowedOrigins: parseCommaSeparated(os.Getenv("OPENTRACE_CORS_ORIGINS")),
		DevMode:            devMode,
		SocketPath:         os.Getenv("OPENTRACE_SOCKET_PATH"),
		DefaultEnv:         envOrDefault("OPENTRACE_DEFAULT_ENV", "production"),
		Performance:        performance,
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
