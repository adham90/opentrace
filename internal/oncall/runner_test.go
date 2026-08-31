package oncall

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func testConfig() Config {
	return Config{
		Enabled:   true,
		Argv:      []string{"fake-agent"},
		MaxPerDay: 10,
		Timeout:   time.Second,
		Cooldown:  0,
	}
}

// fakeExec records what the runner would have executed.
type fakeExec struct {
	calls int
	stdin []byte
	argv  []string
	dir   string
	out   string
	err   error
	delay time.Duration
}

func (f *fakeExec) fn(ctx context.Context, argv []string, dir string, stdin []byte) ([]byte, error) {
	f.calls++
	f.argv = argv
	f.dir = dir
	f.stdin = append([]byte(nil), stdin...)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return []byte(f.out), nil
}

func newTestRunner(t *testing.T, cfg Config, fe *fakeExec) *Runner {
	t.Helper()
	r := New(cfg)
	r.exec = fe.fn
	return r
}

func samplePayload() AlertPayload {
	return AlertPayload{
		Kind:        "watch",
		AlertID:     "alert-1",
		WatchID:     "watch-1",
		Summary:     "error rate 4.2% > 1%",
		Metric:      "error_rate",
		Environment: "production",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
}

func TestRunnerDisabled(t *testing.T) {
	fe := &fakeExec{out: "diagnosis"}
	r := newTestRunner(t, Config{Enabled: false}, fe)

	_, err := r.Run(context.Background(), samplePayload())
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
	if fe.calls != 0 {
		t.Error("a disabled runner executed the agent")
	}
}

func TestRunnerProducesDiagnosis(t *testing.T) {
	fe := &fakeExec{out: "  The checkout endpoint is failing.  \n"}
	r := newTestRunner(t, testConfig(), fe)

	d, err := r.Run(context.Background(), samplePayload())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if d == nil {
		t.Fatal("nil diagnosis")
	}
	if d.Text != "The checkout endpoint is failing." {
		t.Errorf("Text = %q, want it trimmed", d.Text)
	}
	if lastOK, _, _ := r.Status(); lastOK.IsZero() {
		t.Error("a successful run did not record last-success")
	}
}

// The alert must reach the agent on stdin, never on the command line: its
// summary embeds error text from end users.
func TestRunnerPassesAlertOnStdinNotArgv(t *testing.T) {
	fe := &fakeExec{out: "ok"}
	r := newTestRunner(t, testConfig(), fe)

	p := samplePayload()
	p.Summary = "boom: '; rm -rf / #"
	if _, err := r.Run(context.Background(), p); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, a := range fe.argv {
		if strings.Contains(a, "rm -rf") {
			t.Fatalf("alert text leaked into argv: %q", a)
		}
	}
	if !strings.Contains(string(fe.stdin), "rm -rf") {
		t.Error("alert text never reached stdin")
	}
}

// The prompt must mark the payload as untrusted, or an error message reading
// "ignore previous instructions" is indistinguishable from the operator.
func TestRunnerPromptMarksAlertUntrusted(t *testing.T) {
	fe := &fakeExec{out: "ok"}
	r := newTestRunner(t, testConfig(), fe)

	if _, err := r.Run(context.Background(), samplePayload()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	prompt := string(fe.stdin)
	if !strings.Contains(prompt, "untrusted") {
		t.Error("prompt does not mark the alert payload as untrusted")
	}
	if !strings.Contains(prompt, "--- ALERT ---") {
		t.Error("prompt has no boundary between instruction and data")
	}
	if idx := strings.Index(prompt, "--- ALERT ---"); idx < 0 || strings.Contains(prompt[:idx], samplePayload().Summary) {
		t.Error("alert content appears before the boundary marker")
	}
}

func TestRunnerDailyCap(t *testing.T) {
	fe := &fakeExec{out: "ok"}
	cfg := testConfig()
	cfg.MaxPerDay = 2
	r := newTestRunner(t, cfg, fe)

	for i := 0; i < 5; i++ {
		p := samplePayload()
		p.WatchID = "" // distinct keys, so only the cap can suppress
		p.AlertID = string(rune('a' + i))
		if _, err := r.Run(context.Background(), p); err != nil {
			t.Fatalf("Run #%d: %v", i, err)
		}
	}
	if fe.calls != 2 {
		t.Errorf("agent ran %d times, want 2 (the cap)", fe.calls)
	}
}

// A flapping watch must produce one diagnosis, not one per firing.
func TestRunnerCooldownSuppressesRepeats(t *testing.T) {
	fe := &fakeExec{out: "ok"}
	cfg := testConfig()
	cfg.Cooldown = time.Hour
	r := newTestRunner(t, cfg, fe)

	for i := 0; i < 4; i++ {
		if _, err := r.Run(context.Background(), samplePayload()); err != nil {
			t.Fatalf("Run #%d: %v", i, err)
		}
	}
	if fe.calls != 1 {
		t.Errorf("agent ran %d times for the same watch, want 1", fe.calls)
	}
}

// A different watch is a different problem, and must not be suppressed.
func TestRunnerCooldownIsPerAlert(t *testing.T) {
	fe := &fakeExec{out: "ok"}
	cfg := testConfig()
	cfg.Cooldown = time.Hour
	r := newTestRunner(t, cfg, fe)

	a := samplePayload()
	b := samplePayload()
	b.WatchID = "watch-2"

	for _, p := range []AlertPayload{a, b} {
		if _, err := r.Run(context.Background(), p); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	if fe.calls != 2 {
		t.Errorf("agent ran %d times, want 2 (distinct watches)", fe.calls)
	}
}

func TestRunnerTimeoutKillsRun(t *testing.T) {
	fe := &fakeExec{out: "ok", delay: 200 * time.Millisecond}
	cfg := testConfig()
	cfg.Timeout = 20 * time.Millisecond
	r := newTestRunner(t, cfg, fe)

	if _, err := r.Run(context.Background(), samplePayload()); err == nil {
		t.Fatal("expected a timeout error")
	}
	if _, lastErr, _ := r.Status(); lastErr == "" {
		t.Error("failure was not recorded in status")
	}
}

func TestRunnerEmptyOutputIsAnError(t *testing.T) {
	fe := &fakeExec{out: "   \n  "}
	r := newTestRunner(t, testConfig(), fe)

	if _, err := r.Run(context.Background(), samplePayload()); err == nil {
		t.Fatal("expected an error for empty agent output")
	}
}

// A failing sink must not discard the diagnosis — the run succeeded.
func TestRunnerSinkFailureDoesNotFailRun(t *testing.T) {
	fe := &fakeExec{out: "diagnosis"}
	r := newTestRunner(t, testConfig(), fe)
	r.Notify = func(context.Context, Diagnosis) error { return errors.New("telegram down") }
	r.FileIssue = func(context.Context, Diagnosis) error { return errors.New("gh down") }

	d, err := r.Run(context.Background(), samplePayload())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if d == nil || d.Text != "diagnosis" {
		t.Fatalf("diagnosis lost: %+v", d)
	}
}

func TestRunnerExtractsFingerprintFromEvidence(t *testing.T) {
	fe := &fakeExec{out: "ok"}
	r := newTestRunner(t, testConfig(), fe)

	p := samplePayload()
	p.Evidence = &store.WatchEvidenceBundle{
		NewErrors: []store.WatchEvidenceError{{ExceptionClass: "NilError", Fingerprint: "fp-abc"}},
	}

	d, err := r.Run(context.Background(), p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if d.Fingerprint != "fp-abc" {
		t.Errorf("Fingerprint = %q, want fp-abc", d.Fingerprint)
	}
}

func TestRunnerTruncatesRunawayOutput(t *testing.T) {
	fe := &fakeExec{out: strings.Repeat("x", maxDiagnosisBytes*2)}
	r := newTestRunner(t, testConfig(), fe)

	d, err := r.Run(context.Background(), samplePayload())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(d.Text) > maxDiagnosisBytes+32 {
		t.Errorf("len = %d, want it truncated near %d", len(d.Text), maxDiagnosisBytes)
	}
	if !strings.Contains(d.Text, "truncated") {
		t.Error("truncation was silent")
	}
}
