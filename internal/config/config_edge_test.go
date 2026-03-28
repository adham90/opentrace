package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// LoadEnvFile
// ---------------------------------------------------------------------------

func TestLoadEnvFile_MissingFile(t *testing.T) {
	// Should silently return without error when the file does not exist.
	LoadEnvFile("/tmp/definitely-does-not-exist-opentrace-test.env")
}

func TestLoadEnvFile_ValidFile(t *testing.T) {
	content := "MY_TEST_KEY_1=value1\nMY_TEST_KEY_2=value2\n"
	f, err := os.CreateTemp("", "opentrace-env-*.env")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	f.Close()

	// Ensure the keys are not already set.
	t.Setenv("MY_TEST_KEY_1", "")
	os.Unsetenv("MY_TEST_KEY_1")
	t.Setenv("MY_TEST_KEY_2", "")
	os.Unsetenv("MY_TEST_KEY_2")

	LoadEnvFile(f.Name())

	if got := os.Getenv("MY_TEST_KEY_1"); got != "value1" {
		t.Errorf("MY_TEST_KEY_1 = %q, want %q", got, "value1")
	}
	if got := os.Getenv("MY_TEST_KEY_2"); got != "value2" {
		t.Errorf("MY_TEST_KEY_2 = %q, want %q", got, "value2")
	}

	// Clean up env vars set by LoadEnvFile.
	os.Unsetenv("MY_TEST_KEY_1")
	os.Unsetenv("MY_TEST_KEY_2")
}

func TestLoadEnvFile_SkipsCommentsAndEmptyLines(t *testing.T) {
	content := "# This is a comment\n\n   \n# Another comment\nMY_COMMENT_TEST_KEY=hello\n"
	f, err := os.CreateTemp("", "opentrace-env-*.env")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	f.Close()

	os.Unsetenv("MY_COMMENT_TEST_KEY")

	LoadEnvFile(f.Name())

	if got := os.Getenv("MY_COMMENT_TEST_KEY"); got != "hello" {
		t.Errorf("MY_COMMENT_TEST_KEY = %q, want %q", got, "hello")
	}

	os.Unsetenv("MY_COMMENT_TEST_KEY")
}

func TestLoadEnvFile_DoesNotOverrideExisting(t *testing.T) {
	content := "MY_EXISTING_KEY=new-value\n"
	f, err := os.CreateTemp("", "opentrace-env-*.env")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	f.Close()

	// Pre-set the env var; LoadEnvFile must not overwrite it.
	t.Setenv("MY_EXISTING_KEY", "original-value")

	LoadEnvFile(f.Name())

	if got := os.Getenv("MY_EXISTING_KEY"); got != "original-value" {
		t.Errorf("MY_EXISTING_KEY = %q, want %q (should not be overridden)", got, "original-value")
	}
}

func TestLoadEnvFile_SkipsLinesWithoutEquals(t *testing.T) {
	content := "NO_EQUALS_HERE\nVALID_KEY=valid_value\n"
	f, err := os.CreateTemp("", "opentrace-env-*.env")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	f.Close()

	os.Unsetenv("NO_EQUALS_HERE")
	os.Unsetenv("VALID_KEY")

	LoadEnvFile(f.Name())

	// The line without = should not produce any env var.
	if got := os.Getenv("NO_EQUALS_HERE"); got != "" {
		t.Errorf("NO_EQUALS_HERE should be empty, got %q", got)
	}
	if got := os.Getenv("VALID_KEY"); got != "valid_value" {
		t.Errorf("VALID_KEY = %q, want %q", got, "valid_value")
	}

	os.Unsetenv("VALID_KEY")
}

func TestLoadEnvFile_TrimsKeysAndValues(t *testing.T) {
	content := "  TRIM_KEY  =  trim_value  \n"
	f, err := os.CreateTemp("", "opentrace-env-*.env")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	f.Close()

	os.Unsetenv("TRIM_KEY")

	LoadEnvFile(f.Name())

	if got := os.Getenv("TRIM_KEY"); got != "trim_value" {
		t.Errorf("TRIM_KEY = %q, want %q", got, "trim_value")
	}

	os.Unsetenv("TRIM_KEY")
}

// ---------------------------------------------------------------------------
// Load — environment overrides
// ---------------------------------------------------------------------------

func TestLoad_APIKey(t *testing.T) {
	t.Setenv("OPENTRACE_DATA_DIR", t.TempDir())
	t.Setenv("OPENTRACE_API_KEY", "secret-key-123")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIKey != "secret-key-123" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "secret-key-123")
	}
}

func TestLoad_DevMode(t *testing.T) {
	t.Setenv("OPENTRACE_DATA_DIR", t.TempDir())
	t.Setenv("OPENTRACE_DEV", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.DevMode {
		t.Error("DevMode should be true when OPENTRACE_DEV=true")
	}
}

func TestLoad_DevModeFalseByDefault(t *testing.T) {
	t.Setenv("OPENTRACE_DATA_DIR", t.TempDir())
	// OPENTRACE_DEV not set at all.

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DevMode {
		t.Error("DevMode should be false when OPENTRACE_DEV is not set")
	}
}

func TestLoad_DevModeNonTrueString(t *testing.T) {
	t.Setenv("OPENTRACE_DATA_DIR", t.TempDir())
	t.Setenv("OPENTRACE_DEV", "yes") // anything other than "true" should be false

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DevMode {
		t.Error("DevMode should be false for OPENTRACE_DEV=yes (only 'true' is truthy)")
	}
}

func TestLoad_TrustedProxies(t *testing.T) {
	t.Setenv("OPENTRACE_DATA_DIR", t.TempDir())
	t.Setenv("OPENTRACE_TRUSTED_PROXIES", "1.2.3.4,5.6.7.8")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("TrustedProxies length = %d, want 2", len(cfg.TrustedProxies))
	}
	if cfg.TrustedProxies[0] != "1.2.3.4" || cfg.TrustedProxies[1] != "5.6.7.8" {
		t.Errorf("TrustedProxies = %v, want [1.2.3.4 5.6.7.8]", cfg.TrustedProxies)
	}
}

func TestLoad_CORSOrigins(t *testing.T) {
	t.Setenv("OPENTRACE_DATA_DIR", t.TempDir())
	t.Setenv("OPENTRACE_CORS_ORIGINS", "http://localhost,http://example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.CORSAllowedOrigins) != 2 {
		t.Fatalf("CORSAllowedOrigins length = %d, want 2", len(cfg.CORSAllowedOrigins))
	}
	if cfg.CORSAllowedOrigins[0] != "http://localhost" || cfg.CORSAllowedOrigins[1] != "http://example.com" {
		t.Errorf("CORSAllowedOrigins = %v", cfg.CORSAllowedOrigins)
	}
}

func TestLoad_InvalidStatementTimeout(t *testing.T) {
	t.Setenv("OPENTRACE_DATA_DIR", t.TempDir())
	t.Setenv("OPENTRACE_STATEMENT_TIMEOUT_MS", "not-a-number")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid OPENTRACE_STATEMENT_TIMEOUT_MS")
	}
	if !strings.Contains(err.Error(), "OPENTRACE_STATEMENT_TIMEOUT_MS") {
		t.Errorf("error should mention the env var name, got: %v", err)
	}
}

func TestLoad_InvalidMaxQueryRows(t *testing.T) {
	t.Setenv("OPENTRACE_DATA_DIR", t.TempDir())
	t.Setenv("OPENTRACE_MAX_QUERY_ROWS", "abc")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid OPENTRACE_MAX_QUERY_ROWS")
	}
	if !strings.Contains(err.Error(), "OPENTRACE_MAX_QUERY_ROWS") {
		t.Errorf("error should mention the env var name, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// DatabasePath
// ---------------------------------------------------------------------------

func TestDatabasePath_Various(t *testing.T) {
	tests := []struct {
		name    string
		dataDir string
		want    string
	}{
		{
			name:    "absolute path",
			dataDir: "/var/data/opentrace",
			want:    filepath.Join("/var/data/opentrace", "opentrace.db"),
		},
		{
			name:    "home-relative style",
			dataDir: "/home/user/.opentrace",
			want:    filepath.Join("/home/user/.opentrace", "opentrace.db"),
		},
		{
			name:    "current directory",
			dataDir: ".",
			want:    filepath.Join(".", "opentrace.db"),
		},
		{
			name:    "empty data dir",
			dataDir: "",
			want:    "opentrace.db",
		},
		{
			name:    "trailing slash",
			dataDir: "/tmp/data/",
			want:    filepath.Join("/tmp/data/", "opentrace.db"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{DataDir: tt.dataDir}
			if got := cfg.DatabasePath(); got != tt.want {
				t.Errorf("DatabasePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseTrustedProxies
// ---------------------------------------------------------------------------

func TestParseTrustedProxies_Empty(t *testing.T) {
	result := parseTrustedProxies("")
	if result != nil {
		t.Errorf("parseTrustedProxies(\"\") = %v, want nil", result)
	}
}

func TestParseTrustedProxies_SingleValue(t *testing.T) {
	result := parseTrustedProxies("10.0.0.1")
	if len(result) != 1 || result[0] != "10.0.0.1" {
		t.Errorf("parseTrustedProxies(\"10.0.0.1\") = %v, want [10.0.0.1]", result)
	}
}

func TestParseTrustedProxies_MultipleValues(t *testing.T) {
	result := parseTrustedProxies("10.0.0.1,10.0.0.2,10.0.0.3")
	if len(result) != 3 {
		t.Fatalf("got %d elements, want 3", len(result))
	}
	want := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	for i, w := range want {
		if result[i] != w {
			t.Errorf("result[%d] = %q, want %q", i, result[i], w)
		}
	}
}

func TestParseTrustedProxies_Whitespace(t *testing.T) {
	result := parseTrustedProxies("  10.0.0.1 , 10.0.0.2 ,10.0.0.3  ")
	if len(result) != 3 {
		t.Fatalf("got %d elements, want 3", len(result))
	}
	for _, r := range result {
		if strings.TrimSpace(r) != r {
			t.Errorf("value %q still has whitespace", r)
		}
	}
}

func TestParseTrustedProxies_TrailingComma(t *testing.T) {
	result := parseTrustedProxies("10.0.0.1,")
	if len(result) != 1 {
		t.Fatalf("got %d elements, want 1 (trailing comma empty segment filtered)", len(result))
	}
	if result[0] != "10.0.0.1" {
		t.Errorf("result[0] = %q, want %q", result[0], "10.0.0.1")
	}
}

func TestParseTrustedProxies_OnlyCommas(t *testing.T) {
	result := parseTrustedProxies(",,,")
	if len(result) != 0 {
		t.Errorf("parseTrustedProxies(\",,,\") = %v, want empty slice", result)
	}
}

// ---------------------------------------------------------------------------
// parseCORSOrigins
// ---------------------------------------------------------------------------

func TestParseCORSOrigins_Empty(t *testing.T) {
	result := parseCORSOrigins("")
	if result != nil {
		t.Errorf("parseCORSOrigins(\"\") = %v, want nil", result)
	}
}

func TestParseCORSOrigins_SingleValue(t *testing.T) {
	result := parseCORSOrigins("http://localhost:3000")
	if len(result) != 1 || result[0] != "http://localhost:3000" {
		t.Errorf("parseCORSOrigins single = %v", result)
	}
}

func TestParseCORSOrigins_MultipleValues(t *testing.T) {
	result := parseCORSOrigins("http://localhost,https://example.com,https://app.example.com")
	if len(result) != 3 {
		t.Fatalf("got %d elements, want 3", len(result))
	}
	want := []string{"http://localhost", "https://example.com", "https://app.example.com"}
	for i, w := range want {
		if result[i] != w {
			t.Errorf("result[%d] = %q, want %q", i, result[i], w)
		}
	}
}

func TestParseCORSOrigins_Whitespace(t *testing.T) {
	result := parseCORSOrigins(" http://a.com , http://b.com ")
	if len(result) != 2 {
		t.Fatalf("got %d elements, want 2", len(result))
	}
	if result[0] != "http://a.com" || result[1] != "http://b.com" {
		t.Errorf("values not trimmed: %v", result)
	}
}

func TestParseCORSOrigins_TrailingComma(t *testing.T) {
	result := parseCORSOrigins("http://localhost,")
	if len(result) != 1 {
		t.Fatalf("got %d elements, want 1", len(result))
	}
	if result[0] != "http://localhost" {
		t.Errorf("result[0] = %q", result[0])
	}
}

func TestParseCORSOrigins_OnlyCommas(t *testing.T) {
	result := parseCORSOrigins(",,")
	if len(result) != 0 {
		t.Errorf("parseCORSOrigins(\",,\") = %v, want empty slice", result)
	}
}

// ---------------------------------------------------------------------------
// envOrDefaultInt
// ---------------------------------------------------------------------------

func TestEnvOrDefaultInt_EmptyReturnsDefault(t *testing.T) {
	t.Setenv("TEST_ENVINT_EMPTY", "")

	got, err := envOrDefaultInt("TEST_ENVINT_EMPTY", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestEnvOrDefaultInt_UnsetReturnsDefault(t *testing.T) {
	// Ensure the key is not set.
	os.Unsetenv("TEST_ENVINT_UNSET_XYZ")

	got, err := envOrDefaultInt("TEST_ENVINT_UNSET_XYZ", 99)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 99 {
		t.Errorf("got %d, want 99", got)
	}
}

func TestEnvOrDefaultInt_ValidInt(t *testing.T) {
	t.Setenv("TEST_ENVINT_VALID", "256")

	got, err := envOrDefaultInt("TEST_ENVINT_VALID", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 256 {
		t.Errorf("got %d, want 256", got)
	}
}

func TestEnvOrDefaultInt_NegativeInt(t *testing.T) {
	t.Setenv("TEST_ENVINT_NEG", "-10")

	got, err := envOrDefaultInt("TEST_ENVINT_NEG", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != -10 {
		t.Errorf("got %d, want -10", got)
	}
}

func TestEnvOrDefaultInt_Zero(t *testing.T) {
	t.Setenv("TEST_ENVINT_ZERO", "0")

	got, err := envOrDefaultInt("TEST_ENVINT_ZERO", 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestEnvOrDefaultInt_InvalidString(t *testing.T) {
	t.Setenv("TEST_ENVINT_BAD", "hello")

	_, err := envOrDefaultInt("TEST_ENVINT_BAD", 0)
	if err == nil {
		t.Fatal("expected error for non-integer string")
	}
	if !strings.Contains(err.Error(), "TEST_ENVINT_BAD") {
		t.Errorf("error should mention the key name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "hello") {
		t.Errorf("error should mention the invalid value, got: %v", err)
	}
}

func TestEnvOrDefaultInt_FloatString(t *testing.T) {
	t.Setenv("TEST_ENVINT_FLOAT", "3.14")

	_, err := envOrDefaultInt("TEST_ENVINT_FLOAT", 0)
	if err == nil {
		t.Fatal("expected error for float string")
	}
}

// ---------------------------------------------------------------------------
// envOrDefault (indirectly tested via Load, but explicit unit tests here)
// ---------------------------------------------------------------------------

func TestEnvOrDefault_ReturnsEnvValue(t *testing.T) {
	t.Setenv("TEST_EORD_SET", "custom-value")

	got := envOrDefault("TEST_EORD_SET", "default-value")
	if got != "custom-value" {
		t.Errorf("got %q, want %q", got, "custom-value")
	}
}

func TestEnvOrDefault_ReturnsDefault(t *testing.T) {
	os.Unsetenv("TEST_EORD_UNSET_XYZ")

	got := envOrDefault("TEST_EORD_UNSET_XYZ", "fallback")
	if got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}

func TestEnvOrDefault_EmptyEnvReturnsDefault(t *testing.T) {
	t.Setenv("TEST_EORD_EMPTY", "")

	got := envOrDefault("TEST_EORD_EMPTY", "fallback")
	if got != "fallback" {
		t.Errorf("got %q, want %q (empty env should use default)", got, "fallback")
	}
}
