// Package oncall runs the operator's own coding agent against an alert while
// nobody is awake, and delivers the diagnosis instead of the raw threshold.
//
// The agent is not built here. OpenTrace shells out to whatever CLI the
// operator already has — `claude -p`, `codex exec`, anything that reads a
// prompt on stdin and writes an answer on stdout. That keeps three promises
// intact: the binary holds no model credentials, makes no outbound model call
// of its own, and is not tied to one vendor. On a Claude Pro/Max subscription
// (`claude setup-token` on a laptop, CLAUDE_CODE_OAUTH_TOKEN in the unit file)
// the marginal cost to the operator is zero.
//
// It is off by default. Turning it on ships log excerpts to whichever model
// provider the configured command talks to, which is a real change to "no
// external calls" and has to be a deliberate choice.
package oncall

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment variables. All optional; Enabled gates the rest.
const (
	EnabledEnv    = "OPENTRACE_ONCALL_ENABLED"
	CmdEnv        = "OPENTRACE_ONCALL_CMD"
	MaxPerDayEnv  = "OPENTRACE_ONCALL_MAX_PER_DAY"
	TimeoutEnv    = "OPENTRACE_ONCALL_TIMEOUT"
	CooldownEnv   = "OPENTRACE_ONCALL_COOLDOWN"
	WorkspaceEnv  = "OPENTRACE_ONCALL_WORKSPACE"
	GitHubRepoEnv = "OPENTRACE_ONCALL_GITHUB_REPO"
)

// DefaultCmd is Claude Code in headless mode.
//
// No --output-format: stdout is treated as the diagnosis verbatim, so any CLI
// works without OpenTrace knowing its envelope format. No --max-turns either;
// it does not exist in current builds, and spend is bounded by MaxPerDay and
// Timeout instead.
//
// --permission-mode is not optional: without it the process blocks forever on
// the first tool approval, at 3am, with nobody to answer.
const DefaultCmd = "claude -p --permission-mode dontAsk"

// Defaults chosen so a misconfigured or runaway agent cannot spend the night
// burning the operator's quota.
const (
	DefaultMaxPerDay = 10
	DefaultTimeout   = 5 * time.Minute
	DefaultCooldown  = 1 * time.Hour
)

// Config is the on-call runner's configuration.
type Config struct {
	Enabled bool
	// Argv is the command to run, already split. Not passed through a shell:
	// the alert text it is fed is attacker-influenced, and a shell would turn
	// a quoting bug into command execution.
	Argv []string
	// MaxPerDay caps agent invocations per calendar day (UTC).
	MaxPerDay int
	// Timeout bounds a single run. The process is killed when it expires.
	Timeout time.Duration
	// Cooldown suppresses repeat runs for the same alert key, so a flapping
	// watch produces one diagnosis rather than twenty.
	Cooldown time.Duration
	// Workspace is the working directory for the command. The MCP config the
	// CLI picks up lives here; the connect script writes it.
	Workspace string
	// GitHubRepo is "owner/name". Empty disables issue filing.
	GitHubRepo string
}

// LoadConfig reads the on-call configuration from the environment. An invalid
// value is an error rather than a silent fallback: an operator who set a cap
// should not discover at 3am that a typo removed it.
func LoadConfig() (Config, error) {
	cfg := Config{
		Enabled:    os.Getenv(EnabledEnv) == "true",
		MaxPerDay:  DefaultMaxPerDay,
		Timeout:    DefaultTimeout,
		Cooldown:   DefaultCooldown,
		Workspace:  os.Getenv(WorkspaceEnv),
		GitHubRepo: strings.TrimSpace(os.Getenv(GitHubRepoEnv)),
	}
	if !cfg.Enabled {
		return cfg, nil
	}

	cmd := os.Getenv(CmdEnv)
	if strings.TrimSpace(cmd) == "" {
		cmd = DefaultCmd
	}
	cfg.Argv = strings.Fields(cmd)
	if len(cfg.Argv) == 0 {
		return cfg, fmt.Errorf("%s is empty", CmdEnv)
	}

	if v := os.Getenv(MaxPerDayEnv); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return cfg, fmt.Errorf("%s must be a non-negative integer, got %q", MaxPerDayEnv, v)
		}
		cfg.MaxPerDay = n
	}
	if v := os.Getenv(TimeoutEnv); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return cfg, fmt.Errorf("%s must be a positive duration like 5m, got %q", TimeoutEnv, v)
		}
		cfg.Timeout = d
	}
	if v := os.Getenv(CooldownEnv); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return cfg, fmt.Errorf("%s must be a duration like 1h, got %q", CooldownEnv, v)
		}
		cfg.Cooldown = d
	}
	if cfg.GitHubRepo != "" && !strings.Contains(cfg.GitHubRepo, "/") {
		return cfg, fmt.Errorf("%s must be owner/name, got %q", GitHubRepoEnv, cfg.GitHubRepo)
	}
	return cfg, nil
}
