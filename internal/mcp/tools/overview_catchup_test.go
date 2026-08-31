package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

// The shared ErrorGroupStore / WatchStore mocks are inert stubs, and catch-up is
// entirely about which rows fall inside the window — so these doubles apply the
// filters the real stores do.

type catchupErrorGroupStore struct {
	mocks.ErrorGroupStore
	groups []store.ErrorGroup
}

func (s *catchupErrorGroupStore) List(_ context.Context, p store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	var out []store.ErrorGroup
	for _, g := range s.groups {
		if p.Environment != "" && g.Environment != p.Environment {
			continue
		}
		if p.Since != nil && !g.FirstSeenAt.After(*p.Since) {
			continue
		}
		out = append(out, g)
	}
	return out, nil
}

type catchupWatchStore struct {
	mocks.WatchStore
	alerts []store.WatchAlert
}

func (s *catchupWatchStore) ListAlerts(_ context.Context, _ string, status string, limit int) ([]store.WatchAlert, error) {
	var out []store.WatchAlert
	for _, a := range s.alerts {
		if status != "" && a.Status != status {
			continue
		}
		out = append(out, a)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

type catchupFixture struct {
	deps   OverviewDeps
	ctx    context.Context
	userID string
}

func newCatchupFixture(t *testing.T, groups []store.ErrorGroup, alerts []store.WatchAlert, deploys []store.Deploy) catchupFixture {
	t.Helper()

	users := mocks.NewUserStore()
	u, err := users.Create(context.Background(), store.CreateUserParams{Email: "solo@example.com"})
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}

	deployStore := mocks.NewDeployStore()
	for _, d := range deploys {
		if err := deployStore.Record(context.Background(), d); err != nil {
			t.Fatalf("recording deploy: %v", err)
		}
	}

	return catchupFixture{
		deps: OverviewDeps{
			ErrorGroupStore: &catchupErrorGroupStore{groups: groups},
			WatchStore:      &catchupWatchStore{alerts: alerts},
			DeployStore:     deployStore,
			UserStore:       users,
		},
		ctx: envscope.With(context.Background(), envscope.EnvScope{
			Allowed: []string{"production"},
			UserID:  u.ID,
		}),
		userID: u.ID,
	}
}

func decodeCatchup(t *testing.T, res *CallToolResult) map[string]any {
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

func catchupItemTypes(t *testing.T, resp map[string]any) []string {
	t.Helper()
	raw, _ := resp["items"].([]any)
	types := make([]string, 0, len(raw))
	for _, it := range raw {
		m, _ := it.(map[string]any)
		s, _ := m["type"].(string)
		types = append(types, s)
	}
	return types
}

func TestCatchupEmptyIsQuiet(t *testing.T) {
	f := newCatchupFixture(t, nil, nil, nil)

	resp := decodeCatchup(t, mustCatchup(t, f, map[string]any{}))
	if resp["count"].(float64) != 0 {
		t.Errorf("count = %v, want 0", resp["count"])
	}
	if !strings.Contains(resp["summary"].(string), "Nothing new") {
		t.Errorf("summary = %q", resp["summary"])
	}
}

func TestCatchupReportsAllThreeEventKinds(t *testing.T) {
	now := time.Now().UTC()
	f := newCatchupFixture(t,
		[]store.ErrorGroup{{
			Fingerprint: "fp1", Environment: "production", ExceptionClass: "NilError",
			Message: "boom", FirstSeenAt: now.Add(-30 * time.Minute), OccurrenceCount: 12,
		}},
		[]store.WatchAlert{{
			ID: "a1", Summary: "error rate high", Environment: "production",
			Status: "pending", CreatedAt: now.Add(-20 * time.Minute),
		}},
		[]store.Deploy{{
			CommitHash: "abc1234567", Service: "api", Environment: "production",
			FirstSeenAt: now.Add(-40 * time.Minute),
		}},
	)

	resp := decodeCatchup(t, mustCatchup(t, f, map[string]any{}))
	types := catchupItemTypes(t, resp)
	for _, want := range []string{"error_group", "watch_alert", "deploy"} {
		if !contains(types, want) {
			t.Errorf("missing %q in %v", want, types)
		}
	}
}

// Draining the queue must advance the cursor, so the second call is quiet.
func TestCatchupAdvancesCursor(t *testing.T) {
	now := time.Now().UTC()
	f := newCatchupFixture(t, nil, nil, []store.Deploy{{
		CommitHash: "abc", Environment: "production", FirstSeenAt: now.Add(-10 * time.Minute),
	}})

	first := decodeCatchup(t, mustCatchup(t, f, map[string]any{}))
	if first["count"].(float64) != 1 {
		t.Fatalf("first call count = %v, want 1", first["count"])
	}
	if first["cursor_advanced"] != true {
		t.Error("cursor_advanced = false, want true")
	}

	second := decodeCatchup(t, mustCatchup(t, f, map[string]any{}))
	if second["count"].(float64) != 0 {
		t.Errorf("second call count = %v, want 0 — the cursor did not advance", second["count"])
	}
}

// peek is what makes the tool safe to call again after a context compaction.
func TestCatchupPeekDoesNotAdvance(t *testing.T) {
	now := time.Now().UTC()
	f := newCatchupFixture(t, nil, nil, []store.Deploy{{
		CommitHash: "abc", Environment: "production", FirstSeenAt: now.Add(-10 * time.Minute),
	}})

	first := decodeCatchup(t, mustCatchup(t, f, map[string]any{"peek": true}))
	if first["cursor_advanced"] != false {
		t.Error("peek advanced the cursor")
	}

	second := decodeCatchup(t, mustCatchup(t, f, map[string]any{"peek": true}))
	if second["count"].(float64) != 1 {
		t.Errorf("peek lost the event on re-read: count = %v, want 1", second["count"])
	}
}

// Two people on a team each get their own window: one draining must not blank
// the queue for the other.
func TestCatchupCursorIsPerUser(t *testing.T) {
	now := time.Now().UTC()
	f := newCatchupFixture(t, nil, nil, []store.Deploy{{
		CommitHash: "abc", Environment: "production", FirstSeenAt: now.Add(-10 * time.Minute),
	}})

	users := f.deps.UserStore.(*mocks.UserStore)
	other, err := users.Create(context.Background(), store.CreateUserParams{Email: "teammate@example.com"})
	if err != nil {
		t.Fatalf("creating second user: %v", err)
	}

	if resp := decodeCatchup(t, mustCatchup(t, f, map[string]any{})); resp["count"].(float64) != 1 {
		t.Fatalf("first user saw %v events, want 1", resp["count"])
	}

	otherCtx := envscope.With(context.Background(), envscope.EnvScope{
		Allowed: []string{"production"}, UserID: other.ID,
	})
	res, err := HandleCatchup(otherCtx, f.deps, map[string]any{})
	if err != nil {
		t.Fatalf("HandleCatchup: %v", err)
	}
	if resp := decodeCatchup(t, res); resp["count"].(float64) != 1 {
		t.Errorf("teammate saw %v events, want 1 — the cursor is not per user", resp["count"])
	}
}

// An env-scoped token must not catch up on another environment's events.
func TestCatchupRespectsEnvScope(t *testing.T) {
	now := time.Now().UTC()
	f := newCatchupFixture(t,
		[]store.ErrorGroup{
			{Fingerprint: "prod", Environment: "production", Message: "prod boom", FirstSeenAt: now.Add(-10 * time.Minute)},
			{Fingerprint: "stg", Environment: "staging", Message: "staging boom", FirstSeenAt: now.Add(-10 * time.Minute)},
		},
		[]store.WatchAlert{
			{ID: "stg-alert", Summary: "staging alert", Environment: "staging", Status: "pending", CreatedAt: now.Add(-5 * time.Minute)},
		},
		nil,
	)

	resp := decodeCatchup(t, mustCatchup(t, f, map[string]any{}))
	body := extractTextOf(t, resp)
	if strings.Contains(body, "staging") {
		t.Errorf("production-scoped catch-up leaked staging rows: %s", body)
	}
	if resp["count"].(float64) != 1 {
		t.Errorf("count = %v, want 1 (production only)", resp["count"])
	}
}

// Events older than the cursor stay out; that is the whole contract.
func TestCatchupExcludesEventsBeforeCursor(t *testing.T) {
	now := time.Now().UTC()
	f := newCatchupFixture(t, nil, []store.WatchAlert{
		{ID: "old", Summary: "yesterday", Environment: "production", Status: "pending", CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "new", Summary: "just now", Environment: "production", Status: "pending", CreatedAt: now.Add(-5 * time.Minute)},
	}, nil)

	resp := decodeCatchup(t, mustCatchup(t, f, map[string]any{}))
	body := extractTextOf(t, resp)
	if strings.Contains(body, "yesterday") {
		t.Error("an alert older than the first-run window was included")
	}
	if !strings.Contains(body, "just now") {
		t.Error("the recent alert was dropped")
	}
}

// A first-ever call is bounded, and says so rather than pretending the window
// is the caller's own.
func TestCatchupFirstRunIsBounded(t *testing.T) {
	f := newCatchupFixture(t, nil, nil, nil)
	resp := decodeCatchup(t, mustCatchup(t, f, map[string]any{}))

	note, _ := resp["note"].(string)
	if !strings.Contains(note, "first catch-up") {
		t.Errorf("note = %q, want a first-run explanation", note)
	}

	since, err := time.Parse(time.RFC3339, resp["since"].(string))
	if err != nil {
		t.Fatalf("parsing since: %v", err)
	}
	if age := time.Since(since); age > firstCatchupWindow+time.Minute {
		t.Errorf("first-run window = %v, want <= %v", age, firstCatchupWindow)
	}
}

// A cursor from a long holiday is clamped, so the answer stays a queue rather
// than an archive.
func TestCatchupClampsStaleCursor(t *testing.T) {
	f := newCatchupFixture(t, nil, nil, nil)
	stale := time.Now().UTC().Add(-90 * 24 * time.Hour)
	if err := f.deps.UserStore.SetCatchupCursor(context.Background(), f.userID, stale); err != nil {
		t.Fatalf("SetCatchupCursor: %v", err)
	}

	resp := decodeCatchup(t, mustCatchup(t, f, map[string]any{}))
	since, err := time.Parse(time.RFC3339, resp["since"].(string))
	if err != nil {
		t.Fatalf("parsing since: %v", err)
	}
	if age := time.Since(since); age > maxCatchupWindow+time.Minute {
		t.Errorf("window = %v, want clamped to %v", age, maxCatchupWindow)
	}
}

// Without an identity (stdio with no user loaded) the tool still answers rather
// than failing — it just cannot remember where the caller left off.
func TestCatchupWithoutIdentityStillAnswers(t *testing.T) {
	now := time.Now().UTC()
	f := newCatchupFixture(t, nil, nil, []store.Deploy{{
		CommitHash: "abc", Environment: "production", FirstSeenAt: now.Add(-time.Minute),
	}})

	anonCtx := envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"production"}})
	res, err := HandleCatchup(anonCtx, f.deps, map[string]any{})
	if err != nil {
		t.Fatalf("HandleCatchup: %v", err)
	}
	resp := decodeCatchup(t, res)
	if resp["count"].(float64) != 1 {
		t.Errorf("count = %v, want 1", resp["count"])
	}
	if resp["cursor_advanced"] != false {
		t.Error("cursor_advanced = true with no identity to advance")
	}
}

func TestCatchupCapsItems(t *testing.T) {
	now := time.Now().UTC()
	var deploys []store.Deploy
	for i := 0; i < maxCatchupItems+20; i++ {
		deploys = append(deploys, store.Deploy{
			CommitHash:  fmt.Sprintf("commit%03d", i),
			Environment: "production",
			FirstSeenAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	f := newCatchupFixture(t, nil, nil, deploys)

	resp := decodeCatchup(t, mustCatchup(t, f, map[string]any{}))
	if n := int(resp["count"].(float64)); n > maxCatchupItems {
		t.Errorf("count = %d, want <= %d", n, maxCatchupItems)
	}
	if _, ok := resp["truncated"]; !ok {
		t.Error("truncation was not reported")
	}
}

func mustCatchup(t *testing.T, f catchupFixture, args map[string]any) *CallToolResult {
	t.Helper()
	res, err := HandleCatchup(f.ctx, f.deps, args)
	if err != nil {
		t.Fatalf("HandleCatchup: %v", err)
	}
	return res
}

func extractTextOf(t *testing.T, resp map[string]any) string {
	t.Helper()
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
