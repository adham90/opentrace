package tools

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/pkg/store"
)

// envErrorGroupStore is an ErrorGroupStore that actually honours the
// environment argument, which the shared mock does not for List/Count. Without
// that, every assertion below would pass no matter what the handlers did.
type envErrorGroupStore struct {
	groups []store.ErrorGroup

	// lastWrite records the (fingerprint, environment) a lifecycle call
	// targeted, so tests can prove a scoped write did not go blanket.
	lastWriteFingerprint string
	lastWriteEnv         string
	lastWriteSet         bool
}

func (s *envErrorGroupStore) Upsert(context.Context, store.LogEntry) error { return nil }

func (s *envErrorGroupStore) Get(_ context.Context, fingerprint, environment string) (*store.ErrorGroup, error) {
	var newest *store.ErrorGroup
	for i := range s.groups {
		g := &s.groups[i]
		if g.Fingerprint != fingerprint {
			continue
		}
		if environment != "" && g.Environment != environment {
			continue
		}
		if newest == nil || g.LastSeenAt.After(newest.LastSeenAt) {
			newest = g
		}
	}
	if newest == nil {
		return nil, store.ErrNotFound
	}
	clone := *newest
	return &clone, nil
}

func (s *envErrorGroupStore) List(_ context.Context, p store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	var out []store.ErrorGroup
	for _, g := range s.groups {
		if p.Environment != "" && g.Environment != p.Environment {
			continue
		}
		if p.Status != "" && g.Status != p.Status {
			continue
		}
		if p.ActiveSince != nil && g.LastSeenAt.Before(*p.ActiveSince) {
			continue
		}
		if p.Since != nil && g.FirstSeenAt.Before(*p.Since) {
			continue
		}
		out = append(out, g)
	}
	return out, nil
}

func (s *envErrorGroupStore) Count(_ context.Context, status store.ErrorGroupStatus, environment string) (int, error) {
	n := 0
	for _, g := range s.groups {
		if environment != "" && g.Environment != environment {
			continue
		}
		if status != "" && g.Status != status {
			continue
		}
		n++
	}
	return n, nil
}

func (s *envErrorGroupStore) recordWrite(fingerprint, environment string) error {
	s.lastWriteFingerprint = fingerprint
	s.lastWriteEnv = environment
	s.lastWriteSet = true
	return nil
}

func (s *envErrorGroupStore) Resolve(_ context.Context, fingerprint, environment, _ string) error {
	return s.recordWrite(fingerprint, environment)
}

func (s *envErrorGroupStore) Ignore(_ context.Context, fingerprint, environment, _ string) error {
	return s.recordWrite(fingerprint, environment)
}

func (s *envErrorGroupStore) Reopen(_ context.Context, fingerprint, environment, _ string) error {
	return s.recordWrite(fingerprint, environment)
}

func (s *envErrorGroupStore) ListEvents(context.Context, string, string, int) ([]store.ErrorGroupEvent, error) {
	return nil, nil
}

func (s *envErrorGroupStore) Prune(context.Context, time.Duration) (int64, error) { return 0, nil }

var _ store.ErrorGroupStore = (*envErrorGroupStore)(nil)

// sharedFingerprint is one fingerprint present in both envs — the shape that
// broke error detail: production's row is older, so an env-blind
// "ORDER BY last_seen_at DESC LIMIT 1" returns staging's.
func sharedFingerprint() *envErrorGroupStore {
	now := time.Now().UTC()
	return &envErrorGroupStore{
		groups: []store.ErrorGroup{
			{
				Fingerprint: "fp-shared", Environment: "production", Service: "api",
				Message: "prod copy", Status: store.ErrorGroupUnresolved,
				OccurrenceCount: 79,
				FirstSeenAt:     now.Add(-72 * time.Hour), LastSeenAt: now.Add(-48 * time.Hour),
			},
			{
				Fingerprint: "fp-shared", Environment: "staging", Service: "api",
				Message: "staging copy", Status: store.ErrorGroupUnresolved,
				OccurrenceCount: 113,
				FirstSeenAt:     now.Add(-24 * time.Hour), LastSeenAt: now.Add(-1 * time.Minute),
			},
			{
				Fingerprint: "fp-staging-only", Environment: "staging", Service: "api",
				Message: "staging exclusive", Status: store.ErrorGroupUnresolved,
				OccurrenceCount: 16,
				FirstSeenAt:     now.Add(-2 * time.Hour), LastSeenAt: now.Add(-2 * time.Minute),
			},
		},
	}
}

func prodScope(ctx context.Context) context.Context {
	return envscope.With(ctx, envscope.EnvScope{Allowed: []string{"production"}})
}

// A production-scoped caller asking about a fingerprint that exists in both
// envs must get production's row. This is the exact failure that made detail
// unusable: triage listed the fingerprint, detail returned "not found".
func TestErrorsDetail_ScopedTokenReadsItsOwnEnvRow(t *testing.T) {
	deps := ErrorsDeps{ErrorGroupStore: sharedFingerprint()}
	ctx := prodScope(context.Background())

	result, err := ErrorsDetail(ctx, deps, map[string]any{"fingerprint": "fp-shared"})
	if err != nil {
		t.Fatalf("ErrorsDetail: %v", err)
	}
	resp := parseJSON(t, result)

	if got := resp["environment"]; got != "production" {
		t.Errorf("environment = %v, want production", got)
	}
	if got := resp["message"]; got != "prod copy" {
		t.Errorf("message = %v, want the production row", got)
	}
	if got := resp["occurrence_count"]; got != float64(79) {
		t.Errorf("occurrence_count = %v, want production's 79", got)
	}
}

// A fingerprint that exists only in another env stays invisible.
func TestErrorsDetail_DeniesFingerprintFromAnotherEnv(t *testing.T) {
	deps := ErrorsDeps{ErrorGroupStore: sharedFingerprint()}
	ctx := prodScope(context.Background())

	result, err := ErrorsDetail(ctx, deps, map[string]any{"fingerprint": "fp-staging-only"})
	if err != nil {
		t.Fatalf("ErrorsDetail: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected a not-found result for a staging-only fingerprint")
	}
}

// Triage used to query unfiltered, handing a scoped caller fingerprints that
// every drill-down tool would then refuse to open.
func TestTriage_ListsOnlyCallerEnv(t *testing.T) {
	d := OverviewDeps{ErrorGroupStore: sharedFingerprint()}
	ctx := prodScope(context.Background())

	result, err := HandleTriage(ctx, d, map[string]any{})
	if err != nil {
		t.Fatalf("HandleTriage: %v", err)
	}
	resp := parseJSON(t, result)

	items, ok := resp["items"].([]any)
	if !ok {
		t.Fatalf("items missing from triage response: %v", resp)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want only production's row", len(items))
	}
	item := items[0].(map[string]any)
	if got := item["environment"]; got != "production" {
		t.Errorf("environment = %v, want production", got)
	}
	if got := item["id"]; got != "fp-shared" {
		t.Errorf("id = %v, want fp-shared", got)
	}
}

// Resolving from a scoped token must close only that env's row. The blanket
// form would silently close the production incident too.
func TestErrorsResolve_WritesOnlyCallerEnv(t *testing.T) {
	egs := sharedFingerprint()
	deps := ErrorsDeps{ErrorGroupStore: egs}
	ctx := prodScope(context.Background())

	result, err := ErrorsResolve(ctx, deps, map[string]any{
		"fingerprint": "fp-shared",
		"reason":      "fixed in #42",
	})
	if err != nil {
		t.Fatalf("ErrorsResolve: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", extractText(t, result))
	}
	if !egs.lastWriteSet {
		t.Fatal("no lifecycle write reached the store")
	}
	if egs.lastWriteEnv != "production" {
		t.Errorf("write env = %q, want production (blanket writes cross environments)", egs.lastWriteEnv)
	}
}

// A scoped caller must not be able to resolve a group that lives only in
// another env, even though the fingerprint is real.
func TestErrorsResolve_RefusesFingerprintFromAnotherEnv(t *testing.T) {
	egs := sharedFingerprint()
	deps := ErrorsDeps{ErrorGroupStore: egs}
	ctx := prodScope(context.Background())

	result, err := ErrorsResolve(ctx, deps, map[string]any{
		"fingerprint": "fp-staging-only",
		"reason":      "should not apply",
	})
	if err != nil {
		t.Fatalf("ErrorsResolve: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected refusal for a fingerprint outside the caller's env")
	}
	if egs.lastWriteSet {
		t.Errorf("a write reached the store for env %q; nothing should have been mutated", egs.lastWriteEnv)
	}
}

// total_unresolved must describe the same env as the rows returned, otherwise
// the summary disagrees with the listing it summarizes.
func TestErrorsList_CountMatchesListedEnv(t *testing.T) {
	deps := ErrorsDeps{ErrorGroupStore: sharedFingerprint()}
	ctx := prodScope(context.Background())

	result, err := ErrorsList(ctx, deps, map[string]any{})
	if err != nil {
		t.Fatalf("ErrorsList: %v", err)
	}
	resp := parseJSON(t, result)

	if got := resp["total_unresolved"]; got != float64(1) {
		t.Errorf("total_unresolved = %v, want 1 (production only)", got)
	}
	if got := resp["returned"]; got != float64(1) {
		t.Errorf("returned = %v, want 1", got)
	}
}

// since on a listing means "still erroring in this window". Filtering on
// first_seen_at instead hid long-running incidents — the loudest ones.
func TestErrorsList_SinceMatchesStillActiveErrors(t *testing.T) {
	now := time.Now().UTC()
	egs := &envErrorGroupStore{
		groups: []store.ErrorGroup{{
			Fingerprint: "fp-old-but-live", Environment: "production", Service: "api",
			Message: "months old, fired a minute ago", Status: store.ErrorGroupUnresolved,
			OccurrenceCount: 900,
			FirstSeenAt:     now.Add(-90 * 24 * time.Hour), LastSeenAt: now.Add(-1 * time.Minute),
		}},
	}
	deps := ErrorsDeps{ErrorGroupStore: egs}
	ctx := prodScope(context.Background())

	result, err := ErrorsList(ctx, deps, map[string]any{"since": "1h"})
	if err != nil {
		t.Fatalf("ErrorsList: %v", err)
	}
	resp := parseJSON(t, result)

	if got := resp["returned"]; got != float64(1) {
		t.Fatalf("returned = %v, want the still-active group inside a 1h window", got)
	}
}

// diagnose read "timeframe" directly, so since was accepted and ignored: a
// caller asking for 24h silently got a 1h report.
func TestDiagnose_HonoursSince(t *testing.T) {
	d := OverviewDeps{ErrorGroupStore: sharedFingerprint()}
	ctx := prodScope(context.Background())

	result, err := HandleDiagnose(ctx, d, map[string]any{"since": "24h"})
	if err != nil {
		t.Fatalf("HandleDiagnose: %v", err)
	}
	resp := parseJSON(t, result)

	since, err := time.Parse(time.RFC3339, resp["since"].(string))
	if err != nil {
		t.Fatalf("parsing since from response: %v", err)
	}
	window := time.Since(since)
	if window < 23*time.Hour || window > 25*time.Hour {
		t.Errorf("window = %v, want ~24h — since was ignored", window)
	}
}

func TestOptionalSinceParam(t *testing.T) {
	if _, ok := OptionalSinceParam(map[string]any{}); ok {
		t.Error("no window given should report absent")
	}
	if _, ok := OptionalSinceParam(map[string]any{"since": "not-a-duration"}); ok {
		t.Error("an unparseable window should report absent, not a silent default")
	}
	got, ok := OptionalSinceParam(map[string]any{"timeframe": "2h"})
	if !ok {
		t.Fatal("timeframe is a supported legacy name and must be honoured")
	}
	if d := time.Since(got); d < 100*time.Minute || d > 140*time.Minute {
		t.Errorf("timeframe=2h resolved to %v ago", d)
	}
}

// Issue-tracker linkage: these doubles do not exercise it, so the methods exist
// only to satisfy store.ErrorGroupStore.
func (m *envErrorGroupStore) IssueURL(context.Context, string, string) (string, error) {
	return "", nil
}

func (m *envErrorGroupStore) SetIssueURL(context.Context, string, string, string) error { return nil }
