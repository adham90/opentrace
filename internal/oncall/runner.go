package oncall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// maxDiagnosisBytes bounds what is read back from the agent. A CLI stuck in a
// loop can emit megabytes, and the destination is a chat message.
const maxDiagnosisBytes = 16 * 1024

// ErrDisabled is returned when the runner is not configured to run.
var ErrDisabled = errors.New("on-call agent is disabled")

// Diagnosis is the agent's answer plus enough context to route it.
type Diagnosis struct {
	Alert AlertPayload
	Text  string
	// Fingerprint identifies the underlying error group, when the evidence
	// named one. It is what issue filing dedupes on.
	Fingerprint string
	Environment string
	RanFor      time.Duration
}

// Runner invokes the operator's agent CLI for an alert and hands the answer to
// the configured sinks.
//
// Sinks are plain functions rather than an interface: there are exactly two
// (chat and GitHub), both optional, and neither has any other implementation.
type Runner struct {
	cfg Config

	// Notify delivers the diagnosis to a human — the point of the whole
	// feature. Nil means the run is recorded and logged only.
	Notify func(ctx context.Context, d Diagnosis) error
	// FileIssue records the diagnosis somewhere durable. Nil disables it.
	FileIssue func(ctx context.Context, d Diagnosis) error

	// exec is swapped in tests. Production runs the configured argv.
	exec func(ctx context.Context, argv []string, dir string, stdin []byte) ([]byte, error)

	mu        sync.Mutex
	day       string // UTC date the counter belongs to
	runsToday int    // ponytail: in-memory; a restart resets the cap.
	lastRun   map[string]time.Time
	lastOK    time.Time
	lastErr   string
}

// New returns a Runner for the given config.
func New(cfg Config) *Runner {
	return &Runner{
		cfg:     cfg,
		exec:    runCommand,
		lastRun: make(map[string]time.Time),
	}
}

// Enabled reports whether the runner will do anything.
func (r *Runner) Enabled() bool { return r.cfg.Enabled && len(r.cfg.Argv) > 0 }

// Status reports when the agent last succeeded and what went wrong last.
//
// This is the same silence problem as the dead man's switch: the subscription
// token behind the CLI expires eventually, and when it does triage stops with
// no error anyone sees. Surfacing the last success is how that gets noticed.
func (r *Runner) Status() (lastSuccess time.Time, lastError string, runsToday int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastOK, r.lastErr, r.runsToday
}

// Run invokes the agent for one alert. It returns ErrDisabled when off, and a
// nil Diagnosis when the run was suppressed by the daily cap or the cooldown.
func (r *Runner) Run(ctx context.Context, p AlertPayload) (*Diagnosis, error) {
	if !r.Enabled() {
		return nil, ErrDisabled
	}
	if !r.claim(p.DedupeKey()) {
		return nil, nil
	}

	prompt, err := BuildPrompt(p)
	if err != nil {
		r.recordFailure(err)
		return nil, err
	}

	runCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	started := time.Now()
	out, err := r.exec(runCtx, r.cfg.Argv, r.cfg.Workspace, prompt)
	elapsed := time.Since(started)
	if err != nil {
		r.recordFailure(err)
		return nil, fmt.Errorf("on-call agent failed: %w", err)
	}

	text := strings.TrimSpace(string(out))
	if len(text) > maxDiagnosisBytes {
		text = text[:maxDiagnosisBytes] + "\n… (truncated)"
	}
	if text == "" {
		err := errors.New("agent produced no output")
		r.recordFailure(err)
		return nil, err
	}

	d := Diagnosis{
		Alert:       p,
		Text:        text,
		Fingerprint: fingerprintFromEvidence(p),
		Environment: p.Environment,
		RanFor:      elapsed,
	}
	r.recordSuccess()

	// Delivery failures must not discard the diagnosis, so each sink is
	// attempted and logged independently.
	if r.Notify != nil {
		if err := r.Notify(ctx, d); err != nil {
			slog.Error("delivering on-call diagnosis failed", "error", err, "alert_id", p.AlertID)
		}
	}
	if r.FileIssue != nil {
		if err := r.FileIssue(ctx, d); err != nil {
			slog.Error("filing on-call issue failed", "error", err, "alert_id", p.AlertID)
		}
	}
	return &d, nil
}

// claim reserves a slot against the daily cap and the per-alert cooldown.
// Returns false when the run should be skipped.
func (r *Runner) claim(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	today := time.Now().UTC().Format(time.DateOnly)
	if r.day != today {
		r.day = today
		r.runsToday = 0
	}
	if r.cfg.MaxPerDay > 0 && r.runsToday >= r.cfg.MaxPerDay {
		slog.Warn("on-call agent skipped: daily cap reached",
			"cap", r.cfg.MaxPerDay, "alert", key)
		return false
	}
	if r.cfg.Cooldown > 0 && key != "" {
		if last, ok := r.lastRun[key]; ok && time.Since(last) < r.cfg.Cooldown {
			slog.Info("on-call agent skipped: within cooldown", "alert", key, "cooldown", r.cfg.Cooldown)
			return false
		}
		r.lastRun[key] = time.Now()
	}
	r.runsToday++
	return true
}

func (r *Runner) recordSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastOK = time.Now().UTC()
	r.lastErr = ""
}

func (r *Runner) recordFailure(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastErr = err.Error()
}

// fingerprintFromEvidence pulls the error fingerprint out of the evidence
// bundle so issue filing can dedupe on it. Empty when the alert is not about a
// specific error (latency, volume, a health check).
func fingerprintFromEvidence(p AlertPayload) string {
	if p.Evidence == nil {
		return ""
	}
	for _, e := range p.Evidence.NewErrors {
		if e.Fingerprint != "" {
			return e.Fingerprint
		}
	}
	for _, e := range p.Evidence.RecentErrors {
		if e.Fingerprint != "" {
			return e.Fingerprint
		}
	}
	return ""
}

// runCommand executes argv with the prompt on stdin.
//
// No shell: the prompt contains attacker-influenced text, and exec.Command with
// an explicit argv means a quoting mistake stays a quoting mistake instead of
// becoming command execution.
func runCommand(ctx context.Context, argv []string, dir string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("agent timed out after %s", ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}
