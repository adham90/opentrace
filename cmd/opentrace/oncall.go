package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/adham90/opentrace/internal/oncall"
	"github.com/adham90/opentrace/pkg/store"
)

// buildOnCallRunner assembles the on-call agent from the environment, or
// returns nil when it is switched off (the default).
//
// A configuration error is fatal rather than a warning: an operator who set a
// spend cap must not discover at 3am that a typo silently removed it.
func buildOnCallRunner(senders []messageSender, groups store.ErrorGroupStore) (*oncall.Runner, error) {
	cfg, err := oncall.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("on-call configuration: %w", err)
	}
	if !cfg.Enabled {
		return nil, nil
	}

	runner := oncall.New(cfg)
	runner.Notify = func(ctx context.Context, d oncall.Diagnosis) error {
		return broadcast(ctx, senders, formatDiagnosis(d))
	}
	if cfg.GitHubRepo != "" {
		filer := oncall.NewIssueFiler(cfg.GitHubRepo, groups, cfg.Cooldown)
		runner.FileIssue = filer.File
	}

	slog.Warn("on-call agent enabled — alert data will be sent to the configured agent command",
		"command", cfg.Argv[0],
		"max_per_day", cfg.MaxPerDay,
		"timeout", cfg.Timeout,
		"github_repo", cfg.GitHubRepo,
	)
	return runner, nil
}

// broadcast delivers to every configured channel and reports the first failure
// only after trying them all — one dead channel must not silence the others.
func broadcast(ctx context.Context, senders []messageSender, message string) error {
	var firstErr error
	for _, s := range senders {
		if s == nil {
			continue
		}
		if err := s.Send(ctx, message); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// formatDiagnosis renders the agent's answer for a chat channel. The alert line
// comes first so a phone notification preview still says what broke.
func formatDiagnosis(d oncall.Diagnosis) string {
	var b strings.Builder
	b.WriteString("🔎 On-call diagnosis: ")
	b.WriteString(d.Alert.Summary)
	if d.Environment != "" {
		b.WriteString(" [" + d.Environment + "]")
	}
	b.WriteString("\n\n")
	b.WriteString(d.Text)
	return b.String()
}
