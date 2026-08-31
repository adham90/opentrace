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
	"unicode/utf8"

	"github.com/adham90/opentrace/pkg/store"
)

// issueMarkerPrefix is embedded as an HTML comment in every issue body. It
// costs nothing today and is what makes recovery possible if the operator
// restores a database backup and the stored issue URL is gone: the issues can
// still be found with `gh issue list --search "opentrace-fp:<fingerprint>"`.
const issueMarkerPrefix = "opentrace-fp:"

// ghTimeout bounds a single `gh` invocation.
const ghTimeout = 30 * time.Second

// maxIssueTitleLen keeps titles readable in a list view.
const maxIssueTitleLen = 120

// IssueFiler files on-call diagnoses as tracker issues, one per distinct crash.
//
// It files issues and never pull requests, on purpose. Opening a PR would mean
// a checkout, a repo token, a language toolchain, and a test runner living on
// the observability box — a second product. The diagnosis is the valuable part;
// the fix belongs where the code and a human are.
type IssueFiler struct {
	Repo   string
	Groups store.ErrorGroupStore

	// Cooldown suppresses repeat issues for alerts that have no fingerprint to
	// dedupe on (latency, volume, health checks).
	Cooldown time.Duration

	// run is swapped in tests. Production shells out to `gh`.
	run func(ctx context.Context, args []string, stdin []byte) ([]byte, error)

	mu       sync.Mutex
	lastFile map[string]time.Time
}

// NewIssueFiler returns a filer for the given repo. A nil ErrorGroupStore
// disables fingerprint dedupe but still honours the cooldown.
func NewIssueFiler(repo string, groups store.ErrorGroupStore, cooldown time.Duration) *IssueFiler {
	return &IssueFiler{
		Repo:     repo,
		Groups:   groups,
		Cooldown: cooldown,
		run:      runGH,
		lastFile: make(map[string]time.Time),
	}
}

// File records a diagnosis in the tracker.
//
// Dedupe is the difference between this being usable and being noise: a
// flapping error filing six issues overnight is worse than silence, because it
// teaches the operator to ignore the label. A fingerprint we have already filed
// gets a comment on the existing issue; everything else falls back to a
// per-alert cooldown.
func (f *IssueFiler) File(ctx context.Context, d Diagnosis) error {
	if f == nil || f.Repo == "" {
		return nil
	}

	fp := d.Fingerprint
	if fp != "" && f.Groups != nil {
		existing, err := f.Groups.IssueURL(ctx, fp, d.Environment)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("checking for an existing issue: %w", err)
		}
		if existing != "" {
			return f.reopenAndComment(ctx, existing, recurrenceComment(d))
		}
	}

	if !f.claim(d) {
		return nil
	}

	url, err := f.create(ctx, issueTitle(d), issueBody(d, fp))
	if err != nil {
		// Nothing was claimed in the store, so a retry files cleanly. Writing
		// the URL before the tracker confirmed it is the one ordering that can
		// mark a fingerprint filed forever without an issue existing.
		return err
	}
	if fp != "" && f.Groups != nil {
		err := f.Groups.SetIssueURL(ctx, fp, d.Environment, url)
		switch {
		case err == nil:
		case errors.Is(err, store.ErrNotFound):
			// The alert named an error whose group row does not exist yet
			// (ingest has not upserted it, or it was pruned). The issue is
			// real and filed; we just cannot link it, so recurrences fall back
			// to the cooldown. Failing here would be a lie — the work is done.
			slog.Warn("filed an issue with no error group to link it to",
				"issue", url, "fingerprint", fp, "environment", d.Environment)
		default:
			return fmt.Errorf("recording issue url (issue %s was created): %w", url, err)
		}
	}
	return nil
}

// claim applies the cooldown for alerts with no fingerprint to dedupe on.
func (f *IssueFiler) claim(d Diagnosis) bool {
	if f.Cooldown <= 0 {
		return true
	}
	key := d.Alert.DedupeKey()
	if key == "" {
		return true
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if last, ok := f.lastFile[key]; ok && time.Since(last) < f.Cooldown {
		return false
	}
	f.lastFile[key] = time.Now()
	return true
}

func (f *IssueFiler) create(ctx context.Context, title, body string) (string, error) {
	out, err := f.run(ctx, []string{
		"issue", "create",
		"--repo", f.Repo,
		"--title", title,
		"--body-file", "-",
	}, []byte(body))
	if err != nil {
		return "", fmt.Errorf("creating issue: %w", err)
	}
	url := strings.TrimSpace(lastLine(string(out)))
	if url == "" {
		return "", errors.New("gh issue create returned no URL")
	}
	return url, nil
}

// reopenAndComment records a recurrence on the existing issue.
//
// The reopen is what makes this safe over time: once the operator closes the
// issue, a later recurrence would otherwise comment on a closed issue nobody
// is watching — the dedupe would be working perfectly and the alert would still
// be invisible. Reopening an already-open issue is an error from `gh` and is
// deliberately ignored; checking first would cost a round trip to learn
// something the reopen already tells us.
func (f *IssueFiler) reopenAndComment(ctx context.Context, issueURL, body string) error {
	if _, err := f.run(ctx, []string{"issue", "reopen", issueURL, "--repo", f.Repo}, nil); err != nil {
		slog.Debug("reopening issue was not needed or not possible", "issue", issueURL, "error", err)
	}
	return f.comment(ctx, issueURL, body)
}

func (f *IssueFiler) comment(ctx context.Context, issueURL, body string) error {
	if _, err := f.run(ctx, []string{
		"issue", "comment", issueURL,
		"--repo", f.Repo,
		"--body-file", "-",
	}, []byte(body)); err != nil {
		return fmt.Errorf("commenting on %s: %w", issueURL, err)
	}
	return nil
}

func issueTitle(d Diagnosis) string {
	title := d.Alert.Summary
	if title == "" {
		title = "alert fired"
	}
	if d.Environment != "" {
		title = fmt.Sprintf("%s [%s]", title, d.Environment)
	}
	title = "[opentrace] " + strings.ReplaceAll(title, "\n", " ")
	return truncateTitle(title, maxIssueTitleLen)
}

// truncateTitle cuts on a rune boundary and accounts for the ellipsis, so the
// result is both valid UTF-8 and actually within the limit.
func truncateTitle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const ellipsis = "…"
	cut := max - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}

func issueBody(d Diagnosis, fingerprint string) string {
	var b strings.Builder

	b.WriteString(d.Text)
	b.WriteString("\n\n---\n\n")
	b.WriteString("_Filed automatically by the OpenTrace on-call agent. ")
	b.WriteString("The text above is a model's diagnosis of production data, not a verified fix._\n\n")

	fmt.Fprintf(&b, "- Alert: `%s`\n", d.Alert.Summary)
	if d.Alert.Metric != "" {
		fmt.Fprintf(&b, "- Metric: `%s` = %.2f (threshold %.2f)\n",
			d.Alert.Metric, d.Alert.TriggerValue, d.Alert.ThresholdValue)
	}
	if d.Environment != "" {
		fmt.Fprintf(&b, "- Environment: `%s`\n", d.Environment)
	}
	if d.Alert.Service != "" {
		fmt.Fprintf(&b, "- Service: `%s`\n", d.Alert.Service)
	}
	fmt.Fprintf(&b, "- Fired at: %s\n", d.Alert.Timestamp)

	// The marker is what makes this issue findable again after a database
	// restore, so it goes in even when there is no fingerprint to dedupe on.
	marker := fingerprint
	if marker == "" {
		marker = d.Alert.DedupeKey()
	}
	fmt.Fprintf(&b, "\n<!-- %s%s env:%s -->\n", issueMarkerPrefix, marker, d.Environment)
	return b.String()
}

func recurrenceComment(d Diagnosis) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Recurred at %s.\n\n", d.Alert.Timestamp)
	b.WriteString(d.Text)
	return b.String()
}

// runGH shells out to the GitHub CLI. Bodies go over stdin (`--body-file -`)
// rather than argv: they contain a model's prose wrapped around error messages
// from end users, and an argv-sized body is both a quoting hazard and liable to
// blow the argument limit.
func runGH(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, ghTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Stdin = bytes.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
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

// lastLine returns the final non-empty line. `gh issue create` prints the URL
// last, after any progress chatter.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}
