package cliconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// setupConfigDirs points HOME (and therefore the global config path) at a temp
// dir, chdirs into a fresh project dir, and clears the env overrides so Load()
// only sees what the test writes.
func setupConfigDirs(t *testing.T) (projectDir, globalPath string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("OPENTRACE_ENDPOINT", "")
	t.Setenv("OPENTRACE_API_KEY", "")

	gp, err := globalConfigPath()
	if err != nil {
		t.Fatalf("globalConfigPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(gp), 0o755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}

	proj := t.TempDir()
	t.Chdir(proj)
	return proj, gp
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// TestLoad_ProjectOverridesGlobal is the regression test for the inverted
// precedence: a project file that sets api_key but omits endpoint used to be
// overwritten wholesale by the global config.
func TestLoad_ProjectOverridesGlobal(t *testing.T) {
	proj, global := setupConfigDirs(t)

	writeFile(t, global, "endpoint: https://prod.example.com\napi_key: global-key\n")
	writeFile(t, filepath.Join(proj, ProjectConfigFile), "api_key: proj-key\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIKey != "proj-key" {
		t.Errorf("APIKey = %q, want proj-key (project config must win over global)", cfg.APIKey)
	}
	// The project file set no endpoint, so the global one is the next layer.
	if cfg.Endpoint != "https://prod.example.com" {
		t.Errorf("Endpoint = %q, want the global endpoint", cfg.Endpoint)
	}
}

func TestLoad_ProjectEndpointWinsOverGlobal(t *testing.T) {
	proj, global := setupConfigDirs(t)

	writeFile(t, global, "endpoint: https://prod.example.com\napi_key: global-key\n")
	writeFile(t, filepath.Join(proj, ProjectConfigFile), "endpoint: http://localhost:9999\napi_key: proj-key\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Endpoint != "http://localhost:9999" || cfg.APIKey != "proj-key" {
		t.Errorf("got endpoint=%q api_key=%q, want the project values", cfg.Endpoint, cfg.APIKey)
	}
}

func TestLoad_GlobalUsedWhenNoProjectConfig(t *testing.T) {
	_, global := setupConfigDirs(t)

	writeFile(t, global, "endpoint: https://prod.example.com\napi_key: global-key\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Endpoint != "https://prod.example.com" || cfg.APIKey != "global-key" {
		t.Errorf("got endpoint=%q api_key=%q, want the global values", cfg.Endpoint, cfg.APIKey)
	}
}

func TestLoad_EnvOverridesFiles(t *testing.T) {
	proj, global := setupConfigDirs(t)

	writeFile(t, global, "endpoint: https://prod.example.com\napi_key: global-key\n")
	writeFile(t, filepath.Join(proj, ProjectConfigFile), "endpoint: http://localhost:9999\napi_key: proj-key\n")
	t.Setenv("OPENTRACE_ENDPOINT", "http://env:1234")
	t.Setenv("OPENTRACE_API_KEY", "env-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Endpoint != "http://env:1234" || cfg.APIKey != "env-key" {
		t.Errorf("got endpoint=%q api_key=%q, want the env values", cfg.Endpoint, cfg.APIKey)
	}
}

func TestLoad_FallsBackToLocalhost(t *testing.T) {
	setupConfigDirs(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Endpoint != DefaultEndpoint {
		t.Errorf("Endpoint = %q, want %q", cfg.Endpoint, DefaultEndpoint)
	}
}
