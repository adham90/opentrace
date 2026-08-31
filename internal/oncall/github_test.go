package oncall

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

type ghCall struct {
	args  []string
	stdin string
}

type fakeGH struct {
	calls []ghCall
	url   string
	err   error
}

func (f *fakeGH) fn(_ context.Context, args []string, stdin []byte) ([]byte, error) {
	f.calls = append(f.calls, ghCall{args: args, stdin: string(stdin)})
	if f.err != nil {
		return nil, f.err
	}
	url := f.url
	if url == "" {
		url = "https://github.com/acme/app/issues/47"
	}
	return []byte("Creating issue in acme/app\n" + url + "\n"), nil
}

func (f *fakeGH) subcommands() []string {
	var out []string
	for _, c := range f.calls {
		if len(c.args) > 1 {
			out = append(out, c.args[0]+" "+c.args[1])
		}
	}
	return out
}

func newFiler(t *testing.T, groups store.ErrorGroupStore, cooldown time.Duration) (*IssueFiler, *fakeGH) {
	t.Helper()
	gh := &fakeGH{}
	f := NewIssueFiler("acme/app", groups, cooldown)
	f.run = gh.fn
	return f, gh
}

func groupsWith(t *testing.T, fp, env string) *mocks.ErrorGroupStore {
	t.Helper()
	g := mocks.NewErrorGroupStore()
	g.Groups[fp] = &store.ErrorGroup{Fingerprint: fp, Environment: env, Message: "boom"}
	return g
}

func diagnosis(fp, env string) Diagnosis {
	return Diagnosis{
		Alert: AlertPayload{
			Kind: "watch", AlertID: "a1", WatchID: "w1",
			Summary: "error rate 4.2% > 1%", Metric: "error_rate",
			TriggerValue: 4.2, ThresholdValue: 1, Environment: env,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
		Text:        "NilPointerError at payments_controller.rb:87",
		Fingerprint: fp,
		Environment: env,
	}
}

func TestIssueFilerCreatesOnce(t *testing.T) {
	groups := groupsWith(t, "fp1", "production")
	f, gh := newFiler(t, groups, 0)

	if err := f.File(context.Background(), diagnosis("fp1", "production")); err != nil {
		t.Fatalf("File: %v", err)
	}
	if got := gh.subcommands(); len(got) != 1 || got[0] != "issue create" {
		t.Fatalf("calls = %v, want one create", got)
	}
	if url := groups.Groups["fp1"].IssueURL; url == "" {
		t.Error("issue URL was not recorded on the error group")
	}
}

// The whole point: the same crash recurring comments rather than filing again.
func TestIssueFilerCommentsOnRecurrence(t *testing.T) {
	groups := groupsWith(t, "fp1", "production")
	f, gh := newFiler(t, groups, 0)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := f.File(ctx, diagnosis("fp1", "production")); err != nil {
			t.Fatalf("File #%d: %v", i, err)
		}
	}

	got := gh.subcommands()
	if got[0] != "issue create" {
		t.Errorf("first call = %q, want issue create", got[0])
	}
	creates, comments := 0, 0
	for _, c := range got {
		switch c {
		case "issue create":
			creates++
		case "issue comment":
			comments++
		}
	}
	if creates != 1 {
		t.Errorf("creates = %d, want 1 — recurrences must not file again", creates)
	}
	if comments != 2 {
		t.Errorf("comments = %d, want 2", comments)
	}
}

// A failed create must leave nothing claimed, so a retry files cleanly. The
// opposite ordering marks the fingerprint filed forever with no issue behind it.
func TestIssueFilerFailedCreateLeavesNothingClaimed(t *testing.T) {
	groups := groupsWith(t, "fp1", "production")
	f, gh := newFiler(t, groups, 0)
	gh.err = errors.New("gh: not authenticated")

	if err := f.File(context.Background(), diagnosis("fp1", "production")); err == nil {
		t.Fatal("expected an error")
	}
	if url := groups.Groups["fp1"].IssueURL; url != "" {
		t.Fatalf("issue URL = %q after a failed create — the fingerprint is now permanently marked filed", url)
	}

	// And the retry works.
	gh.err = nil
	f.Cooldown = 0
	if err := f.File(context.Background(), diagnosis("fp1", "production")); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if groups.Groups["fp1"].IssueURL == "" {
		t.Error("retry did not record the issue URL")
	}
}

// The same crash in two environments is two problems and two issues.
func TestIssueFilerSeparatesEnvironments(t *testing.T) {
	// One error_groups row per env, which is how the real store is keyed.
	prod := mocks.NewErrorGroupStore()
	prod.Groups["fp1"] = &store.ErrorGroup{Fingerprint: "fp1", Environment: "production"}
	staging := mocks.NewErrorGroupStore()
	staging.Groups["fp1"] = &store.ErrorGroup{Fingerprint: "fp1", Environment: "staging"}

	gh := &fakeGH{}
	fProd := NewIssueFiler("acme/app", prod, 0)
	fProd.run = gh.fn
	fStaging := NewIssueFiler("acme/app", staging, 0)
	fStaging.run = gh.fn
	f := fProd

	if err := f.File(context.Background(), diagnosis("fp1", "production")); err != nil {
		t.Fatalf("File(production): %v", err)
	}
	if err := fStaging.File(context.Background(), diagnosis("fp1", "staging")); err != nil {
		t.Fatalf("File(staging): %v", err)
	}
	if prod.Groups["fp1"].IssueURL == "" || staging.Groups["fp1"].IssueURL == "" {
		t.Error("both env rows should carry their own issue URL")
	}

	creates := 0
	for _, c := range gh.subcommands() {
		if c == "issue create" {
			creates++
		}
	}
	if creates != 2 {
		t.Errorf("creates = %d, want 2 (one per env)", creates)
	}
}

// Latency and health-check alerts have no fingerprint, so the cooldown is the
// only thing standing between a flapping watch and a page of duplicates.
func TestIssueFilerCooldownForFingerprintlessAlerts(t *testing.T) {
	f, gh := newFiler(t, nil, time.Hour)
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		if err := f.File(ctx, diagnosis("", "production")); err != nil {
			t.Fatalf("File #%d: %v", i, err)
		}
	}
	if n := len(gh.calls); n != 1 {
		t.Errorf("gh called %d times, want 1 (cooldown)", n)
	}
}

func TestIssueFilerBodyCarriesRecoveryMarker(t *testing.T) {
	groups := groupsWith(t, "fp-abc", "production")
	f, gh := newFiler(t, groups, 0)

	if err := f.File(context.Background(), diagnosis("fp-abc", "production")); err != nil {
		t.Fatalf("File: %v", err)
	}
	body := gh.calls[0].stdin
	if !strings.Contains(body, issueMarkerPrefix+"fp-abc") {
		t.Errorf("body has no recovery marker:\n%s", body)
	}
	if !strings.Contains(body, "env:production") {
		t.Error("marker does not record the environment")
	}
	if !strings.Contains(body, "not a verified fix") {
		t.Error("body does not disclose that the diagnosis is model output")
	}
}

// Bodies must go over stdin: they wrap error text from end users, and argv is
// both a quoting hazard and length-limited.
func TestIssueFilerSendsBodyOnStdin(t *testing.T) {
	groups := groupsWith(t, "fp1", "production")
	f, gh := newFiler(t, groups, 0)

	d := diagnosis("fp1", "production")
	d.Text = "backtick ` and quote ' and $(whoami)"
	if err := f.File(context.Background(), d); err != nil {
		t.Fatalf("File: %v", err)
	}

	for _, a := range gh.calls[0].args {
		if strings.Contains(a, "whoami") {
			t.Fatalf("body leaked into argv: %q", a)
		}
	}
	if !strings.Contains(gh.calls[0].stdin, "whoami") {
		t.Error("body never reached stdin")
	}
	if !containsArg(gh.calls[0].args, "--body-file") {
		t.Error("gh was not told to read the body from a file/stdin")
	}
}

func TestIssueFilerTitleIsBounded(t *testing.T) {
	groups := groupsWith(t, "fp1", "production")
	f, gh := newFiler(t, groups, 0)

	d := diagnosis("fp1", "production")
	d.Alert.Summary = strings.Repeat("very long summary ", 40)
	if err := f.File(context.Background(), d); err != nil {
		t.Fatalf("File: %v", err)
	}

	title := argValue(gh.calls[0].args, "--title")
	if len(title) > maxIssueTitleLen {
		t.Errorf("title length = %d, want <= %d", len(title), maxIssueTitleLen)
	}
	if strings.Contains(title, "\n") {
		t.Error("title contains a newline")
	}
}

func TestIssueFilerNoRepoIsNoop(t *testing.T) {
	f := NewIssueFiler("", mocks.NewErrorGroupStore(), 0)
	called := false
	f.run = func(context.Context, []string, []byte) ([]byte, error) {
		called = true
		return nil, nil
	}
	if err := f.File(context.Background(), diagnosis("fp1", "production")); err != nil {
		t.Fatalf("File: %v", err)
	}
	if called {
		t.Error("gh was invoked with no repo configured")
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// An error group that has not been upserted yet still gets an issue: the
// diagnosis is what matters, and failing here would report the run as broken
// when the issue is sitting in the tracker.
func TestIssueFilerFilesEvenWithNoErrorGroupRow(t *testing.T) {
	f, gh := newFiler(t, mocks.NewErrorGroupStore(), 0)

	if err := f.File(context.Background(), diagnosis("unknown-fp", "production")); err != nil {
		t.Fatalf("File: %v", err)
	}
	if len(gh.calls) != 1 {
		t.Fatalf("gh calls = %d, want 1", len(gh.calls))
	}
}

// Once the operator closes the issue, a recurrence must reopen it. Commenting
// on a closed issue is dedupe working perfectly and the alert still being
// invisible.
func TestIssueFilerReopensOnRecurrence(t *testing.T) {
	groups := groupsWith(t, "fp1", "production")
	f, gh := newFiler(t, groups, 0)
	ctx := context.Background()

	if err := f.File(ctx, diagnosis("fp1", "production")); err != nil {
		t.Fatalf("File: %v", err)
	}
	if err := f.File(ctx, diagnosis("fp1", "production")); err != nil {
		t.Fatalf("File (recurrence): %v", err)
	}

	var reopened bool
	for _, c := range gh.subcommands() {
		if c == "issue reopen" {
			reopened = true
		}
	}
	if !reopened {
		t.Errorf("recurrence did not reopen the issue: %v", gh.subcommands())
	}
}

// A reopen that fails (the issue is already open) must not stop the comment.
func TestIssueFilerCommentsEvenIfReopenFails(t *testing.T) {
	groups := groupsWith(t, "fp1", "production")
	gh := &failingReopenGH{}
	f := NewIssueFiler("acme/app", groups, 0)
	f.run = gh.fn
	ctx := context.Background()

	if err := f.File(ctx, diagnosis("fp1", "production")); err != nil {
		t.Fatalf("File: %v", err)
	}
	if err := f.File(ctx, diagnosis("fp1", "production")); err != nil {
		t.Fatalf("File (recurrence): %v", err)
	}
	if !gh.commented {
		t.Error("a failing reopen swallowed the recurrence comment")
	}
}

type failingReopenGH struct {
	commented bool
}

func (g *failingReopenGH) fn(_ context.Context, args []string, _ []byte) ([]byte, error) {
	if len(args) > 1 && args[1] == "reopen" {
		return nil, errors.New("gh: issue is already open")
	}
	if len(args) > 1 && args[1] == "comment" {
		g.commented = true
	}
	return []byte("https://github.com/acme/app/issues/47\n"), nil
}
