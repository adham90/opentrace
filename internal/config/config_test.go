package config

import (
	"os"
	"testing"
)

func clearEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"OPENTRACE_DATA_DIR",
		"OPENTRACE_LISTEN_ADDR",
		"OPENTRACE_MAX_QUERY_ROWS",
		"OPENTRACE_STATEMENT_TIMEOUT_MS",
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
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "127.0.0.1:8080")
	}
	if cfg.MaxQueryRows != 500 {
		t.Errorf("MaxQueryRows = %d, want %d", cfg.MaxQueryRows, 500)
	}
	if cfg.StatementTimeoutMS != 5000 {
		t.Errorf("StatementTimeoutMS = %d, want %d", cfg.StatementTimeoutMS, 5000)
	}
}

func TestLoad_AllOverrides(t *testing.T) {
	clearEnv(t)
	overrides := map[string]string{
		"OPENTRACE_DATA_DIR":             "/tmp/opentrace-test",
		"OPENTRACE_LISTEN_ADDR":          ":9090",
		"OPENTRACE_MAX_QUERY_ROWS":       "1000",
		"OPENTRACE_STATEMENT_TIMEOUT_MS": "10000",
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
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.MaxQueryRows != 1000 {
		t.Errorf("MaxQueryRows = %d", cfg.MaxQueryRows)
	}
	if cfg.StatementTimeoutMS != 10000 {
		t.Errorf("StatementTimeoutMS = %d", cfg.StatementTimeoutMS)
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
