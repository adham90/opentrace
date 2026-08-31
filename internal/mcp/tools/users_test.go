package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

// userImpactDouble is a behavioural double: the shared mock returns nils, and every
// assertion here is about which rows come back for which caller.
type userImpactDouble struct {
	mocks.ErrorImpactStore
	userErrors   map[string][]store.ErrorSummary
	affected     map[string][]store.AffectedUser
	impact       map[string]*store.ErrorImpact
	lastSinceArg time.Time
	lastLimitArg int
}

func (s *userImpactDouble) GetUserErrors(_ context.Context, userID string, since time.Time) ([]store.ErrorSummary, error) {
	s.lastSinceArg = since
	return s.userErrors[userID], nil
}

func (s *userImpactDouble) GetAffectedUsers(_ context.Context, fingerprint string, limit int) ([]store.AffectedUser, error) {
	s.lastLimitArg = limit
	return s.affected[fingerprint], nil
}

func (s *userImpactDouble) GetImpact(_ context.Context, fingerprint, _ string) (*store.ErrorImpact, error) {
	if i, ok := s.impact[fingerprint]; ok {
		return i, nil
	}
	return nil, store.ErrNotFound
}

func prodCtx() context.Context {
	return envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"production"}})
}

func decodeUsers(t *testing.T, res *CallToolResult) map[string]any {
	t.Helper()
	if res == nil {
		t.Fatal("nil result")
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", extractText(t, res))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(extractText(t, res)), &out); err != nil {
		t.Fatalf("decoding response: %v\n%s", err, extractText(t, res))
	}
	return out
}

func TestUserTimelineRequiresSubject(t *testing.T) {
	d := UsersDeps{LogStore: mocks.NewLogStore()}

	res, err := HandleUserTimeline(prodCtx(), d, map[string]any{})
	if err != nil {
		t.Fatalf("HandleUserTimeline: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("expected an error when neither user_id nor tenant_id is given")
	}
}

// The filter has to reach the store. Applied after the fact it would be both
// wrong (the limit truncates before filtering) and pointlessly expensive.
func TestUserTimelinePushesFilterToStore(t *testing.T) {
	ls := mocks.NewLogStore()
	d := UsersDeps{LogStore: ls}

	if _, err := HandleUserTimeline(prodCtx(), d, map[string]any{"user_id": "u-42"}); err != nil {
		t.Fatalf("HandleUserTimeline: %v", err)
	}
	if got := ls.LastSearchParams.UserID; got != "u-42" {
		t.Errorf("UserID filter = %q, want u-42", got)
	}
	if got := ls.LastSearchParams.Environment; got != "production" {
		t.Errorf("Environment = %q, want production (the caller's scope)", got)
	}
	if ls.LastSearchParams.Start == nil {
		t.Error("no time window was applied — the default must bound the scan")
	}
	if got := ls.LastSearchParams.Limit; got != defaultUsersLimit {
		t.Errorf("Limit = %d, want %d", got, defaultUsersLimit)
	}
}

// tenant_id is the B2B version of the same question and must reach the store
// as its own filter, not as a user id.
func TestUserTimelineSupportsTenant(t *testing.T) {
	ls := mocks.NewLogStore()
	d := UsersDeps{LogStore: ls}

	if _, err := HandleUserTimeline(prodCtx(), d, map[string]any{"tenant_id": "acme"}); err != nil {
		t.Fatalf("HandleUserTimeline: %v", err)
	}
	if got := ls.LastSearchParams.TenantID; got != "acme" {
		t.Errorf("TenantID filter = %q, want acme", got)
	}
	if ls.LastSearchParams.UserID != "" {
		t.Errorf("UserID = %q, want empty", ls.LastSearchParams.UserID)
	}
}

func TestUserTimelineCapsLimit(t *testing.T) {
	ls := mocks.NewLogStore()
	d := UsersDeps{LogStore: ls}

	if _, err := HandleUserTimeline(prodCtx(), d, map[string]any{
		"user_id": "u1", "limit": float64(100000),
	}); err != nil {
		t.Fatalf("HandleUserTimeline: %v", err)
	}
	if got := ls.LastSearchParams.Limit; got != maxUsersLimit {
		t.Errorf("Limit = %d, want it capped at %d", got, maxUsersLimit)
	}
}

func TestUserTimelineRendersEntries(t *testing.T) {
	ls := mocks.NewLogStore()
	now := time.Now()
	ls.Entries = []store.LogEntry{
		{ID: 1, Timestamp: now, Level: "error", Service: "api", Message: "checkout failed",
			ExceptionClass: "NilError", ErrorFingerprint: "fp1", TraceID: "t1"},
		{ID: 2, Timestamp: now.Add(-time.Minute), Level: "info", Service: "api", Message: "GET /cart",
			RequestSummary: &store.RequestSummary{Method: "GET", Path: "/cart", Status: 200, DurationMs: 42}},
	}
	d := UsersDeps{LogStore: ls}

	res, err := HandleUserTimeline(prodCtx(), d, map[string]any{"user_id": "u1"})
	if err != nil {
		t.Fatalf("HandleUserTimeline: %v", err)
	}
	resp := decodeUsers(t, res)

	if resp["count"].(float64) != 2 {
		t.Errorf("count = %v, want 2", resp["count"])
	}
	if resp["error_count"].(float64) != 1 {
		t.Errorf("error_count = %v, want 1", resp["error_count"])
	}
	body, _ := json.Marshal(resp)
	for _, want := range []string{"checkout failed", "NilError", "/cart"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("response missing %q", want)
		}
	}
}

func TestUserTimelineEmptyIsExplained(t *testing.T) {
	d := UsersDeps{LogStore: mocks.NewLogStore()}

	res, err := HandleUserTimeline(prodCtx(), d, map[string]any{"user_id": "ghost"})
	if err != nil {
		t.Fatalf("HandleUserTimeline: %v", err)
	}
	resp := decodeUsers(t, res)
	if s, _ := resp["summary"].(string); !strings.Contains(s, "No activity") {
		t.Errorf("summary = %q", s)
	}
}

// A malformed window must be reported, not silently swapped for the default —
// that is how a caller reads an unrelated window's numbers as the answer.
func TestUserTimelineRejectsBadWindow(t *testing.T) {
	d := UsersDeps{LogStore: mocks.NewLogStore()}

	res, err := HandleUserTimeline(prodCtx(), d, map[string]any{"user_id": "u1", "since": "1w"})
	if err != nil {
		t.Fatalf("HandleUserTimeline: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("a malformed since was accepted")
	}
}

func TestUserErrorsRequiresUserID(t *testing.T) {
	d := UsersDeps{ErrorImpactStore: &userImpactDouble{}}

	res, err := HandleUserErrors(prodCtx(), d, map[string]any{})
	if err != nil {
		t.Fatalf("HandleUserErrors: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("expected an error without user_id")
	}
}

func TestUserErrorsReturnsGroups(t *testing.T) {
	is := &userImpactDouble{userErrors: map[string][]store.ErrorSummary{
		"u1": {{Fingerprint: "fp1", ExceptionClass: "NilError", Message: "boom", OccurrenceCount: 3}},
	}}
	d := UsersDeps{ErrorImpactStore: is}

	res, err := HandleUserErrors(prodCtx(), d, map[string]any{"user_id": "u1"})
	if err != nil {
		t.Fatalf("HandleUserErrors: %v", err)
	}
	resp := decodeUsers(t, res)
	if resp["count"].(float64) != 1 {
		t.Fatalf("count = %v, want 1", resp["count"])
	}
	if is.lastSinceArg.IsZero() {
		t.Error("no time window reached the store")
	}
}

// Reading who an error affects must not be a way around env scope.
func TestUserImpactRefusesOutOfScopeFingerprint(t *testing.T) {
	groups := mocks.NewErrorGroupStore()
	groups.Groups["fp-staging"] = &store.ErrorGroup{Fingerprint: "fp-staging", Environment: "staging"}
	is := &userImpactDouble{affected: map[string][]store.AffectedUser{
		"fp-staging": {{UserID: "victim", OccurrenceCount: 9}},
	}}
	d := UsersDeps{ErrorImpactStore: is, ErrorGroupStore: groups}

	res, err := HandleUserImpact(prodCtx(), d, map[string]any{"fingerprint": "fp-staging"})
	if err != nil {
		t.Fatalf("HandleUserImpact: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("a production-scoped caller read a staging error's users: %s", extractText(t, res))
	}
}

func TestUserImpactReturnsAffectedUsers(t *testing.T) {
	groups := mocks.NewErrorGroupStore()
	groups.Groups["fp1"] = &store.ErrorGroup{Fingerprint: "fp1", Environment: "production"}
	is := &userImpactDouble{
		affected: map[string][]store.AffectedUser{
			"fp1": {{UserID: "u1", OccurrenceCount: 9}, {UserID: "u2", OccurrenceCount: 2}},
		},
		impact: map[string]*store.ErrorImpact{
			"fp1": {Fingerprint: "fp1", UniqueUsers: 2, TotalOccurrences: 11, ImpactScore: 7.5},
		},
	}
	d := UsersDeps{ErrorImpactStore: is, ErrorGroupStore: groups}

	res, err := HandleUserImpact(prodCtx(), d, map[string]any{"fingerprint": "fp1"})
	if err != nil {
		t.Fatalf("HandleUserImpact: %v", err)
	}
	resp := decodeUsers(t, res)
	if resp["count"].(float64) != 2 {
		t.Errorf("count = %v, want 2", resp["count"])
	}
	if resp["unique_users"].(float64) != 2 {
		t.Errorf("unique_users = %v, want 2", resp["unique_users"])
	}
	if is.lastLimitArg <= 0 || is.lastLimitArg > maxUsersLimit {
		t.Errorf("limit passed to store = %d, want a bounded default", is.lastLimitArg)
	}
}

func TestUsersHandlerRejectsUnknownAction(t *testing.T) {
	h := UsersHandler(UsersDeps{LogStore: mocks.NewLogStore()})

	res, err := h(prodCtx(), MakeCallToolRequest("users", map[string]any{"action": "delete"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("unknown action was accepted")
	}
}
