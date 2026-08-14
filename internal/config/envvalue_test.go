package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseEnvValue(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "ot_abc123", "ot_abc123"},
		{"double quoted", `"ot_abc123"`, "ot_abc123"},
		{"single quoted", `'ot_abc123'`, "ot_abc123"},
		{"quoted with spaces", `"hello world"`, "hello world"},
		{"quoted then comment", `"ot_abc123" # the api key`, "ot_abc123"},
		{"inline comment", "ot_abc123 # the api key", "ot_abc123"},
		{"hash without leading space is kept", "http://x/y#frag", "http://x/y#frag"},
		{"comment char inside quotes is kept", `"pa#ss"`, "pa#ss"},
		{"single quotes are literal", `'a\nb'`, `a\nb`},
		{"double quote escapes expand", `"a\nb"`, "a\nb"},
		{"escaped quote", `"say \"hi\""`, `say "hi"`},
		{"unterminated quote stays literal", `"oops`, `"oops`},
		{"empty", "   ", ""},
		{"empty quoted", `""`, ""},
		{"surrounding whitespace trimmed", "  value  ", "value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseEnvValue(tt.in); got != tt.want {
				t.Errorf("parseEnvValue(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestLoadEnvFile_QuotedAndCommentedValues is the end-to-end version: a
// conventionally quoted secret must reach the environment unquoted.
func TestLoadEnvFile_QuotedAndCommentedValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "OPENTRACE_TEST_KEY=\"ot_abc123\"\n" +
		"OPENTRACE_TEST_ADDR=127.0.0.1:8080 # listen here\n" +
		"OPENTRACE_TEST_SINGLE='sq_value'\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing .env: %v", err)
	}

	for _, k := range []string{"OPENTRACE_TEST_KEY", "OPENTRACE_TEST_ADDR", "OPENTRACE_TEST_SINGLE"} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}

	LoadEnvFile(path)

	want := map[string]string{
		"OPENTRACE_TEST_KEY":    "ot_abc123",
		"OPENTRACE_TEST_ADDR":   "127.0.0.1:8080",
		"OPENTRACE_TEST_SINGLE": "sq_value",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}
