package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// impactStore — an ErrorImpactStore whose queries are fingerprint-only and
// env-blind, exactly like the sqlite one. Any env scoping the tests observe
// therefore has to come from the handler.
// ---------------------------------------------------------------------------

type impactStore struct {
	impact  *store.ErrorImpact
	users   []store.AffectedUser
	byUser  map[string][]store.ErrorSummary
	ranking []store.ErrorGroupWithImpact

	// gotEnv is the environment the last GetImpact call asked for.
	gotEnv string
}

var _ store.ErrorImpactStore = (*impactStore)(nil)

func (s *impactStore) TrackImpact(context.Context, string, string, string, map[string]any, int64, string) error {
	return nil
}

// GetImpact records the environment it was asked for. An env-pinned caller
// must request its OWN env rather than the cross-env aggregate, because the
// aggregate reports only the highest-impact env — gating on that env denies a
// caller whose env simply scores lower.
func (s *impactStore) GetImpact(_ context.Context, fingerprint, environment string) (*store.ErrorImpact, error) {
	s.gotEnv = environment
	if s.impact == nil {
		return &store.ErrorImpact{Fingerprint: fingerprint, Environment: environment}, nil
	}
	clone := *s.impact
	clone.Fingerprint = fingerprint
	if environment != "" {
		clone.Environment = environment
	}
	return &clone, nil
}

func (s *impactStore) GetAffectedUsers(context.Context, string, int) ([]store.AffectedUser, error) {
	return s.users, nil
}

func (s *impactStore) GetUserErrors(_ context.Context, userID string, _ time.Time) ([]store.ErrorSummary, error) {
	return s.byUser[userID], nil
}

func (s *impactStore) ComputeImpactScores(context.Context) error { return nil }

func (s *impactStore) TopByImpact(_ context.Context, _ store.ImpactQueryParams) ([]store.ErrorGroupWithImpact, error) {
	// Env-blind on purpose: the sqlite implementation ignores
	// ImpactQueryParams.Environment entirely.
	return s.ranking, nil
}

func (s *impactStore) FindCommonTraits(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func stagingScopedCtx() context.Context {
	return envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"staging"}})
}

// twoEnvGroups has the same shape as production: one fingerprint per env.
func twoEnvGroups() *envErrorGroupStore {
	now := time.Now().UTC()
	return &envErrorGroupStore{groups: []store.ErrorGroup{
		{Fingerprint: "fp-prod", Environment: "production", Service: "api",
			Message: "prod boom", Status: store.ErrorGroupUnresolved,
			FirstSeenAt: now.Add(-48 * time.Hour), LastSeenAt: now.Add(-2 * time.Hour)},
		{Fingerprint: "fp-stg", Environment: "staging", Service: "api",
			Message: "staging boom", Status: store.ErrorGroupUnresolved,
			FirstSeenAt: now.Add(-48 * time.Hour), LastSeenAt: now.Add(-2 * time.Hour)},
	}}
}

// ---------------------------------------------------------------------------
// Issue 4: ranking must not leak other environments
// ---------------------------------------------------------------------------

func TestErrorsRanking_EnvScopedTokenSeesOnlyItsOwnEnv(t *testing.T) {
	now := time.Now().UTC()
	is := &impactStore{ranking: []store.ErrorGroupWithImpact{
		{ErrorGroup: store.ErrorGroup{Fingerprint: "fp-prod", Environment: "production",
			Service: "api", Message: "prod boom", LastSeenAt: now},
			TopAffectedUsers: []store.AffectedUser{{UserID: "prod-customer-42"}}},
		{ErrorGroup: store.ErrorGroup{Fingerprint: "fp-stg", Environment: "staging",
			Service: "api", Message: "staging boom", LastSeenAt: now},
			TopAffectedUsers: []store.AffectedUser{{UserID: "stg-user-1"}}},
		{ErrorGroup: store.ErrorGroup{Fingerprint: "fp-unknown-env", Environment: "",
			Service: "api", Message: "legacy row", LastSeenAt: now}},
	}}
	deps := ErrorsDeps{ErrorImpactStore: is, ErrorGroupStore: twoEnvGroups()}

	result, err := ErrorsRanking(stagingScopedCtx(), deps, map[string]any{"action": "ranking"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractText(t, result)
	if strings.Contains(text, "fp-prod") || strings.Contains(text, "prod boom") {
		t.Fatalf("production error group leaked to a staging-scoped token: %s", text)
	}
	if strings.Contains(text, "prod-customer-42") {
		t.Fatalf("production user ID leaked to a staging-scoped token: %s", text)
	}
	if strings.Contains(text, "fp-unknown-env") {
		t.Fatalf("row with no environment must not be shown to a pinned token: %s", text)
	}
	if !strings.Contains(text, "fp-stg") {
		t.Fatalf("staging's own error group must still be returned: %s", text)
	}
}

func TestErrorsRanking_UnscopedCallerUnaffected(t *testing.T) {
	now := time.Now().UTC()
	is := &impactStore{ranking: []store.ErrorGroupWithImpact{
		{ErrorGroup: store.ErrorGroup{Fingerprint: "fp-prod", Environment: "production", LastSeenAt: now}},
	}}
	deps := ErrorsDeps{ErrorImpactStore: is}

	result, err := ErrorsRanking(context.Background(), deps, map[string]any{"action": "ranking"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(extractText(t, result), "fp-prod") {
		t.Errorf("a caller with no scope attached should still see results: %s", extractText(t, result))
	}
}

// ---------------------------------------------------------------------------
// Issue 3: user_errors must not enumerate another env's errors
// ---------------------------------------------------------------------------

func TestErrorsUserErrors_EnvScopedTokenSeesOnlyItsOwnEnv(t *testing.T) {
	now := time.Now().UTC()
	is := &impactStore{byUser: map[string][]store.ErrorSummary{
		"prod-customer-42": {
			{Fingerprint: "fp-prod", Message: "prod boom", Status: store.ErrorGroupUnresolved,
				FirstSeenAt: now.Add(-time.Hour), LastSeenAt: now},
			{Fingerprint: "fp-stg", Message: "staging boom", Status: store.ErrorGroupUnresolved,
				FirstSeenAt: now.Add(-time.Hour), LastSeenAt: now},
		},
	}}
	deps := ErrorsDeps{ErrorImpactStore: is, ErrorGroupStore: twoEnvGroups()}

	result, err := ErrorsUserErrors(stagingScopedCtx(), deps, map[string]any{
		"action": "user_errors", "user_id": "prod-customer-42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractText(t, result)
	if strings.Contains(text, "fp-prod") || strings.Contains(text, "prod boom") {
		t.Fatalf("production error history leaked to a staging-scoped token: %s", text)
	}
	if !strings.Contains(text, "fp-stg") {
		t.Fatalf("staging's own errors must still be returned: %s", text)
	}
}

func TestErrorsUserErrors_DoesNotSuggestItself(t *testing.T) {
	now := time.Now().UTC()
	is := &impactStore{byUser: map[string][]store.ErrorSummary{
		"u1": {{Fingerprint: "fp-stg", Message: "boom", FirstSeenAt: now, LastSeenAt: now}},
	}}
	deps := ErrorsDeps{ErrorImpactStore: is, ErrorGroupStore: twoEnvGroups()}

	result, err := ErrorsUserErrors(context.Background(), deps, map[string]any{
		"action": "user_errors", "user_id": "u1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(extractText(t, result), `"action":"user_errors"`) {
		t.Errorf("user_errors must not suggest re-running itself: %s", extractText(t, result))
	}
}

// ---------------------------------------------------------------------------
// Issue 2: impact must not be broken closed for env-scoped tokens
// ---------------------------------------------------------------------------

func TestErrorsImpact_ScopedTokenReadsItsOwnFingerprint(t *testing.T) {
	is := &impactStore{impact: &store.ErrorImpact{UniqueUsers: 3, TotalOccurrences: 9}}
	deps := ErrorsDeps{
		ErrorImpactStore: is,
		ErrorGroupStore:  twoEnvGroups(),
	}

	result, err := ErrorsImpact(stagingScopedCtx(), deps, map[string]any{
		"action": "impact", "fingerprint": "fp-stg",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("a staging token must be able to read a staging fingerprint's impact: %s", extractText(t, result))
	}
	// A pinned token must be served its own env's numbers, not the cross-env
	// aggregate: the aggregate names only the highest-impact env, so gating on
	// that env used to deny a caller whose env simply scored lower.
	if is.gotEnv != "staging" {
		t.Errorf("GetImpact env = %q, want staging — a pinned token must ask for its own env", is.gotEnv)
	}
	if strings.Contains(extractText(t, result), "scope_note") {
		t.Errorf("env-scoped numbers must not be labelled a cross-env aggregate: %s", extractText(t, result))
	}
}

// An unpinned (wildcard) caller keeps the all-env aggregate, and it must stay
// labelled as one so its totals are never read as a single env's number.
func TestErrorsImpact_UnpinnedCallerGetsLabelledAggregate(t *testing.T) {
	is := &impactStore{impact: &store.ErrorImpact{UniqueUsers: 3, TotalOccurrences: 9}}
	deps := ErrorsDeps{ErrorImpactStore: is, ErrorGroupStore: twoEnvGroups()}

	result, err := ErrorsImpact(context.Background(), deps, map[string]any{
		"action": "impact", "fingerprint": "fp-stg",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("an unscoped caller must be able to read impact: %s", extractText(t, result))
	}
	if is.gotEnv != "" {
		t.Errorf("GetImpact env = %q, want \"\" — an unpinned caller keeps the aggregate", is.gotEnv)
	}
	if !strings.Contains(extractText(t, result), "scope_note") {
		t.Errorf("a cross-env aggregate must say so: %s", extractText(t, result))
	}
}

func TestErrorsImpact_ScopedTokenDeniedForOtherEnvFingerprint(t *testing.T) {
	deps := ErrorsDeps{
		ErrorImpactStore: &impactStore{impact: &store.ErrorImpact{UniqueUsers: 3}},
		ErrorGroupStore:  twoEnvGroups(),
	}

	result, err := ErrorsImpact(stagingScopedCtx(), deps, map[string]any{
		"action": "impact", "fingerprint": "fp-prod",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("a staging token must not read a production-only fingerprint: %s", extractText(t, result))
	}
}

func TestErrorsImpact_HonoursPopulatedImpactEnvironment(t *testing.T) {
	deps := ErrorsDeps{
		// The sqlite agent is populating Environment; once set it is the gate.
		ErrorImpactStore: &impactStore{impact: &store.ErrorImpact{Environment: "production", UniqueUsers: 3}},
		ErrorGroupStore:  twoEnvGroups(),
	}
	result, err := ErrorsImpact(stagingScopedCtx(), deps, map[string]any{
		"action": "impact", "fingerprint": "fp-prod",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("impact.Environment=production must be denied for a staging token: %s", extractText(t, result))
	}
}

// ---------------------------------------------------------------------------
// Issue 5: investigate for an anchor older than an hour
// ---------------------------------------------------------------------------

func TestErrorsInvestigate_OldTraceIDStillResolves(t *testing.T) {
	ls := oldAnchorStore()
	deps := ErrorsDeps{LogStore: ls}

	result, err := ErrorsInvestigate(context.Background(), deps, map[string]any{
		"action": "investigate", "trace_id": "trace-old",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("a 3h-old trace must resolve, got: %s", extractText(t, result))
	}
	resp := parseResult(t, result)
	if resp["trace_timeline"] == nil {
		t.Errorf("expected trace_timeline for an old anchor, got keys %v", keysOf(resp))
	}
	logs, _ := resp["surrounding_logs"].([]any)
	sawBefore := false
	for _, l := range logs {
		if l.(map[string]any)["position"] == "before" {
			sawBefore = true
		}
	}
	if !sawBefore {
		t.Errorf("expected before-context for an anchor older than 1h, got %v", logs)
	}
}

func TestErrorsInvestigate_OldLogIDHasContextAndTimeline(t *testing.T) {
	ls := oldAnchorStore()
	deps := ErrorsDeps{LogStore: ls}

	result, err := ErrorsInvestigate(context.Background(), deps, map[string]any{
		"action": "investigate", "log_id": float64(100),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseResult(t, result)
	if resp["trace_timeline"] == nil {
		t.Errorf("expected trace_timeline, got keys %v", keysOf(resp))
	}
	if resp["surrounding_logs"] == nil {
		t.Errorf("expected surrounding_logs, got keys %v", keysOf(resp))
	}
}

// ---------------------------------------------------------------------------
// Issue 11: recent_occurrences for a group that last fired days ago
// ---------------------------------------------------------------------------

func TestErrorsDetail_RecentOccurrencesForOldGroup(t *testing.T) {
	now := time.Now().UTC()
	lastSeen := now.Add(-48 * time.Hour)
	ls := &windowLogStore{entries: []store.LogEntry{
		{ID: 7, Timestamp: lastSeen, Level: "error", Service: "api", Environment: "production",
			Message: "boom", ErrorFingerprint: "fp-old", TraceID: "t1"},
	}}
	groups := &envErrorGroupStore{groups: []store.ErrorGroup{{
		Fingerprint: "fp-old", Environment: "production", Service: "api",
		Status: store.ErrorGroupUnresolved, OccurrenceCount: 3,
		FirstSeenAt: now.Add(-72 * time.Hour), LastSeenAt: lastSeen,
	}}}
	deps := ErrorsDeps{ErrorGroupStore: groups, LogStore: ls}

	result, err := ErrorsDetail(context.Background(), deps, map[string]any{
		"action": "detail", "fingerprint": "fp-old", "environment": "production",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseResult(t, result)
	if resp["recent_occurrences"] == nil {
		t.Fatalf("a group whose last occurrence is 2 days old must still list occurrences, got keys %v", keysOf(resp))
	}
}

// ---------------------------------------------------------------------------
// Issue 19 (errors list): a malformed since must error
// ---------------------------------------------------------------------------

func TestErrorsList_MalformedSinceIsRejected(t *testing.T) {
	deps := ErrorsDeps{ErrorGroupStore: twoEnvGroups()}
	result, err := ErrorsList(context.Background(), deps, map[string]any{
		"action": "list", "since": "1w",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("since=\"1w\" must be rejected rather than silently returning the unbounded listing: %s",
			extractText(t, result))
	}
}
