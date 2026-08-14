// Package cliconfig provides .opentrace.yml detection and resolution for CLI commands.
// It walks up from cwd looking for the config file, then falls back to env vars,
// global config, and finally localhost.
package cliconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	// ProjectConfigFile is the name of the project-level config file.
	ProjectConfigFile = ".opentrace.yml"
	// GlobalConfigDir is the directory for global config.
	GlobalConfigDir = "opentrace"
	// GlobalConfigFile is the name of the global config file.
	GlobalConfigFile = "config.yml"
	// DefaultEndpoint is the fallback endpoint when no config is found.
	DefaultEndpoint = "http://localhost:8080"
)

// ProjectConfig holds the CLI connection settings.
type ProjectConfig struct {
	Endpoint    string `yaml:"endpoint" json:"endpoint"`
	APIKey      string `yaml:"api_key" json:"api_key"`
	Service     string `yaml:"service,omitempty" json:"service,omitempty"`
	Environment string `yaml:"environment,omitempty" json:"environment,omitempty"`
}

// Load resolves CLI config using this precedence:
//  1. OPENTRACE_ENDPOINT / OPENTRACE_API_KEY env vars
//  2. .opentrace.yml in current directory or any parent
//  3. ~/.config/opentrace/config.yml (global default)
//  4. http://localhost:8080 (fallback)
//
// Flag overrides (--endpoint, --api-key) are applied by the caller after Load.
func Load() (*ProjectConfig, error) {
	cfg := &ProjectConfig{}

	// Global config first: it is the lowest-precedence file layer, so the
	// project file (and then env vars) overwrite whatever it sets. Loading it
	// afterwards instead — gated on an empty endpoint — let a global api_key
	// clobber a project api_key whenever the project file omitted endpoint.
	if path, err := globalConfigPath(); err == nil {
		loadYAML(path, cfg) //nolint:errcheck // file may not exist
	}

	// Project config file (walk up from cwd) overrides the global one. Only
	// keys actually present in the file are overwritten by yaml.Unmarshal.
	if path, err := findProjectConfig(); err == nil {
		if err := loadYAML(path, cfg); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
	}

	// Env vars override file config
	if v := os.Getenv("OPENTRACE_ENDPOINT"); v != "" {
		cfg.Endpoint = v
	}
	if v := os.Getenv("OPENTRACE_API_KEY"); v != "" {
		cfg.APIKey = v
	}

	// Fallback
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}

	return cfg, nil
}

// findProjectConfig walks up from cwd looking for .opentrace.yml.
func findProjectConfig() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		path := filepath.Join(dir, ProjectConfigFile)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("no %s found", ProjectConfigFile)
}

// globalConfigPath returns ~/.config/opentrace/config.yml.
func globalConfigPath() (string, error) {
	home, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, GlobalConfigDir, GlobalConfigFile), nil
}

// loadYAML reads a YAML file into the given struct.
func loadYAML(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, v)
}
