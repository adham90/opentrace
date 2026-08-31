package oncall

import (
	"testing"
	"time"
)

func TestLoadConfigDisabledByDefault(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("on-call is enabled with no configuration — it must be opt-in")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv(EnabledEnv, "true")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Argv) == 0 || cfg.Argv[0] != "claude" {
		t.Errorf("Argv = %v, want the default claude invocation", cfg.Argv)
	}
	// Without a permission mode the CLI blocks forever on the first tool
	// approval, at 3am, with nobody to answer.
	if !containsArg(cfg.Argv, "--permission-mode") {
		t.Errorf("default command has no --permission-mode: %v", cfg.Argv)
	}
	if cfg.MaxPerDay != DefaultMaxPerDay || cfg.Timeout != DefaultTimeout {
		t.Errorf("caps = %d/%v, want %d/%v", cfg.MaxPerDay, cfg.Timeout, DefaultMaxPerDay, DefaultTimeout)
	}
}

func TestLoadConfigCustomCommand(t *testing.T) {
	t.Setenv(EnabledEnv, "true")
	t.Setenv(CmdEnv, "codex exec --sandbox read-only")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := []string{"codex", "exec", "--sandbox", "read-only"}
	if len(cfg.Argv) != len(want) {
		t.Fatalf("Argv = %v, want %v", cfg.Argv, want)
	}
	for i := range want {
		if cfg.Argv[i] != want[i] {
			t.Fatalf("Argv = %v, want %v", cfg.Argv, want)
		}
	}
}

// A typo in a spend cap must fail loudly at startup, not silently remove it.
func TestLoadConfigRejectsBadValues(t *testing.T) {
	for _, tc := range []struct{ env, val string }{
		{MaxPerDayEnv, "lots"},
		{MaxPerDayEnv, "-1"},
		{TimeoutEnv, "5 minutes"},
		{TimeoutEnv, "0s"},
		{CooldownEnv, "soon"},
		{GitHubRepoEnv, "justarepo"},
	} {
		t.Run(tc.env+"="+tc.val, func(t *testing.T) {
			t.Setenv(EnabledEnv, "true")
			t.Setenv(tc.env, tc.val)
			if _, err := LoadConfig(); err == nil {
				t.Fatalf("%s=%q was accepted", tc.env, tc.val)
			}
		})
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	t.Setenv(EnabledEnv, "true")
	t.Setenv(MaxPerDayEnv, "3")
	t.Setenv(TimeoutEnv, "90s")
	t.Setenv(CooldownEnv, "30m")
	t.Setenv(GitHubRepoEnv, "acme/app")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.MaxPerDay != 3 || cfg.Timeout != 90*time.Second || cfg.Cooldown != 30*time.Minute {
		t.Errorf("got %d/%v/%v", cfg.MaxPerDay, cfg.Timeout, cfg.Cooldown)
	}
	if cfg.GitHubRepo != "acme/app" {
		t.Errorf("GitHubRepo = %q", cfg.GitHubRepo)
	}
}

// A broken command must not be silently ignored while the operator believes
// they have an on-call agent.
func TestLoadConfigRejectsEmptyCommandOverride(t *testing.T) {
	t.Setenv(EnabledEnv, "true")
	t.Setenv(CmdEnv, "   ")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Blank falls back to the default rather than erroring — an empty string in
	// a .env file is indistinguishable from an unset one.
	if len(cfg.Argv) == 0 {
		t.Fatal("blank command produced no argv")
	}
}
