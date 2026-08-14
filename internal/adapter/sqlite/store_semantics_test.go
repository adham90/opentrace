package sqlite

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// seedErrorGroup writes one error_groups row for (fingerprint, environment).
func seedErrorGroup(t *testing.T, egs store.ErrorGroupStore, fingerprint, environment string) {
	t.Helper()
	if err := egs.Upsert(context.Background(), store.LogEntry{
		ID:               1,
		Level:            "error",
		Timestamp:        time.Now().UTC(),
		ErrorFingerprint: fingerprint,
		Service:          "api",
		Environment:      environment,
		ExceptionClass:   "Boom",
		Message:          "boom",
	}); err != nil {
		t.Fatalf("seed error group (%s, %s): %v", fingerprint, environment, err)
	}
}

// findGroup returns the listed group for one env.
func findGroup(t *testing.T, groups []store.ErrorGroup, environment string) store.ErrorGroup {
	t.Helper()
	for _, g := range groups {
		if g.Environment == environment {
			return g
		}
	}
	t.Fatalf("no group for environment %q in %+v", environment, groups)
	return store.ErrorGroup{}
}

// TestComputeImpactScores_IsPerEnvironment covers the cross-env aggregation
// bug: error_groups is keyed (fingerprint, environment), so a fingerprint hit
// by many staging testers must not stamp their count and score onto the
// production row.
func TestComputeImpactScores_IsPerEnvironment(t *testing.T) {
	db := setupTestDB(t)
	egs := NewErrorGroupStore(db)
	eis := NewErrorImpactStore(db)
	ctx := context.Background()

	seedErrorGroup(t, egs, "fp-multi", "staging")
	seedErrorGroup(t, egs, "fp-multi", "production")

	for i := 0; i < 5; i++ {
		if err := eis.TrackImpact(ctx, "fp-multi", "staging",
			fmt.Sprintf("tester-%d", i), map[string]any{"env": "staging"}, int64(i), "api"); err != nil {
			t.Fatalf("TrackImpact staging: %v", err)
		}
	}
	if err := eis.TrackImpact(ctx, "fp-multi", "production",
		"real-user", map[string]any{"env": "production"}, 99, "api"); err != nil {
		t.Fatalf("TrackImpact production: %v", err)
	}

	if err := eis.ComputeImpactScores(ctx); err != nil {
		t.Fatalf("ComputeImpactScores: %v", err)
	}

	groups, err := egs.List(ctx, store.ListErrorGroupParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	prod := findGroup(t, groups, "production")
	staging := findGroup(t, groups, "staging")

	if prod.UniqueUsers != 1 {
		t.Errorf("production unique_users = %d, want 1 (staging testers must not count)", prod.UniqueUsers)
	}
	if staging.UniqueUsers != 5 {
		t.Errorf("staging unique_users = %d, want 5", staging.UniqueUsers)
	}
	if prod.ImpactScore == staging.ImpactScore {
		t.Errorf("both envs scored %v — the aggregate is not per-environment", prod.ImpactScore)
	}
	if got, ok := prod.CommonContext["env"].(map[string]any); ok {
		if _, leaked := got["staging"]; leaked {
			t.Errorf("production common_context carries staging traits: %+v", prod.CommonContext)
		}
	}
}

// TestComputeImpactScores_KeepsTraitsOutsideBudget covers the traits wipe:
// groups ranked below the trait budget used to have their common_context
// overwritten with "{}" on every run.
func TestComputeImpactScores_KeepsTraitsOutsideBudget(t *testing.T) {
	db := setupTestDB(t)
	egs := NewErrorGroupStore(db)
	eis := NewErrorImpactStore(db)
	ctx := context.Background()

	// maxTraitComputations high-impact groups, plus one deliberately tiny one
	// that will sort last.
	for i := 0; i < maxTraitComputations; i++ {
		fp := fmt.Sprintf("fp-top-%02d", i)
		seedErrorGroup(t, egs, fp, "production")
		for u := 0; u < 3; u++ {
			if err := eis.TrackImpact(ctx, fp, "production",
				fmt.Sprintf("u-%d", u), map[string]any{"browser": "Chrome"}, 1, "api"); err != nil {
				t.Fatalf("TrackImpact: %v", err)
			}
		}
	}
	seedErrorGroup(t, egs, "fp-tail", "production")
	if err := eis.TrackImpact(ctx, "fp-tail", "production", "lonely",
		map[string]any{"browser": "Safari"}, 1, "api"); err != nil {
		t.Fatalf("TrackImpact tail: %v", err)
	}

	// Traits previously computed for the tail group.
	if _, err := db.NewRaw(
		`UPDATE error_groups SET common_context = ? WHERE fingerprint = ?`,
		`{"browser":{"Safari":1}}`, "fp-tail",
	).Exec(ctx); err != nil {
		t.Fatalf("seed common_context: %v", err)
	}

	if err := eis.ComputeImpactScores(ctx); err != nil {
		t.Fatalf("ComputeImpactScores: %v", err)
	}

	tail, err := egs.Get(ctx, "fp-tail", "production")
	if err != nil {
		t.Fatalf("Get fp-tail: %v", err)
	}
	if len(tail.CommonContext) == 0 {
		t.Error("common_context was wiped for a group outside the trait budget")
	}
	if tail.UniqueUsers != 1 {
		t.Errorf("tail unique_users = %d, want 1 — scores must still be updated", tail.UniqueUsers)
	}
}

// TestGetImpact_PopulatesEnvironment covers the empty ErrorImpact.Environment:
// the MCP env-scope gate reads it, so leaving it blank rejected every scoped
// caller.
func TestGetImpact_PopulatesEnvironment(t *testing.T) {
	db := setupTestDB(t)
	egs := NewErrorGroupStore(db)
	eis := NewErrorImpactStore(db)
	ctx := context.Background()

	seedErrorGroup(t, egs, "fp-env", "production")
	if err := eis.TrackImpact(ctx, "fp-env", "production", "user-1", nil, 1, "api"); err != nil {
		t.Fatalf("TrackImpact: %v", err)
	}

	impact, err := eis.GetImpact(ctx, "fp-env", "")
	if err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if impact.Environment != "production" {
		t.Errorf("Environment = %q, want production", impact.Environment)
	}

	// With rows in several envs, the reported env is the one whose score the
	// aggregate view reports, so a caller can fetch the matching group.
	seedErrorGroup(t, egs, "fp-env", "staging")
	if err := eis.TrackImpact(ctx, "fp-env", "staging", "user-2", nil, 2, "api"); err != nil {
		t.Fatalf("TrackImpact staging: %v", err)
	}
	if _, err := db.NewRaw(
		`UPDATE error_groups SET impact_score = ? WHERE fingerprint = ? AND environment = ?`,
		9.5, "fp-env", "staging",
	).Exec(ctx); err != nil {
		t.Fatalf("set staging score: %v", err)
	}
	impact, err = eis.GetImpact(ctx, "fp-env", "")
	if err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if impact.Environment != "staging" || impact.ImpactScore != 9.5 {
		t.Errorf("Environment/ImpactScore = %q/%v, want staging/9.5 (highest-impact env)",
			impact.Environment, impact.ImpactScore)
	}
}

// TestErrorGroupStore_ResolveIsIdempotent covers the 404-for-an-existing-group
// bug: re-resolving an already-resolved group returned store.ErrNotFound.
func TestErrorGroupStore_ResolveIsIdempotent(t *testing.T) {
	db := setupTestDB(t)
	egs := NewErrorGroupStore(db)
	ctx := context.Background()

	seedErrorGroup(t, egs, "fp-idem", "production")

	if err := egs.Resolve(ctx, "fp-idem", "production", "fixed"); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	if err := egs.Resolve(ctx, "fp-idem", "production", "fixed again"); err != nil {
		t.Fatalf("second Resolve of an already-resolved group: %v", err)
	}
	if err := egs.Ignore(ctx, "fp-idem", "production", "noisy"); err != nil {
		t.Fatalf("Ignore: %v", err)
	}
	if err := egs.Ignore(ctx, "fp-idem", "production", "still noisy"); err != nil {
		t.Fatalf("second Ignore: %v", err)
	}

	// A genuinely absent group must still be ErrNotFound.
	if err := egs.Resolve(ctx, "fp-nope", "production", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Resolve(unknown fingerprint) = %v, want ErrNotFound", err)
	}
	if err := egs.Resolve(ctx, "fp-idem", "staging", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Resolve(known fingerprint, unknown env) = %v, want ErrNotFound", err)
	}

	// The no-op second call must not add lifecycle noise.
	events, err := egs.ListEvents(ctx, "fp-idem", "production", 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("got %d events, want 2 (one resolved, one ignored): %+v", len(events), events)
	}
}

// TestErrorGroupStore_BlanketResolveOnlyLogsTransitions covers the spurious
// events: a blanket resolve used to log an event for every env row, including
// ones the UPDATE skipped because they were already resolved.
func TestErrorGroupStore_BlanketResolveOnlyLogsTransitions(t *testing.T) {
	db := setupTestDB(t)
	egs := NewErrorGroupStore(db)
	ctx := context.Background()

	seedErrorGroup(t, egs, "fp-blanket", "production")
	seedErrorGroup(t, egs, "fp-blanket", "staging")

	if err := egs.Resolve(ctx, "fp-blanket", "production", "fixed in prod"); err != nil {
		t.Fatalf("scoped Resolve: %v", err)
	}
	if err := egs.Resolve(ctx, "fp-blanket", "", "fixed everywhere"); err != nil {
		t.Fatalf("blanket Resolve: %v", err)
	}

	prodEvents, err := egs.ListEvents(ctx, "fp-blanket", "production", 10)
	if err != nil {
		t.Fatalf("ListEvents(production): %v", err)
	}
	if len(prodEvents) != 1 {
		t.Errorf("production events = %d, want 1 — the blanket resolve did not change production: %+v",
			len(prodEvents), prodEvents)
	}
	stagingEvents, err := egs.ListEvents(ctx, "fp-blanket", "staging", 10)
	if err != nil {
		t.Fatalf("ListEvents(staging): %v", err)
	}
	if len(stagingEvents) != 1 {
		t.Errorf("staging events = %d, want 1", len(stagingEvents))
	}

	// Both rows still end up resolved.
	groups, _ := egs.List(ctx, store.ListErrorGroupParams{})
	for _, g := range groups {
		if g.Status != store.ErrorGroupResolved {
			t.Errorf("env %q status = %q, want resolved", g.Environment, g.Status)
		}
	}
}

// TestTraceStore_DurationUsesSpanTimestamps covers duration_ms being computed
// from ingestion wall-clock: spans stamped 50ms apart but shipped 30s late used
// to produce a ~30s trace.
func TestTraceStore_DurationUsesSpanTimestamps(t *testing.T) {
	db := setupTestDB(t)
	ts := NewTraceStore(db)
	ctx := context.Background()

	// Truncated to a second because RFC3339 storage is second-granular: the
	// first span then lands exactly on the stored first_seen_at.
	const shippingLag = 30 * time.Second
	base := time.Now().UTC().Add(-shippingLag).Truncate(time.Second)

	for _, offset := range []time.Duration{0, 50 * time.Millisecond} {
		if err := ts.UpsertTraceStatus(ctx, "trace-1", store.LogEntry{
			Level:     "info",
			Service:   "api",
			SpanID:    fmt.Sprintf("span-%d", offset),
			Timestamp: base.Add(offset),
		}); err != nil {
			t.Fatalf("UpsertTraceStatus: %v", err)
		}
	}

	status, err := ts.GetTraceStatus(ctx, "trace-1")
	if err != nil {
		t.Fatalf("GetTraceStatus: %v", err)
	}
	if status.DurationMs != 50 {
		t.Errorf("duration_ms = %v, want 50 for two spans 50ms apart shipped %s late — ingestion lag must not leak into the duration",
			status.DurationMs, shippingLag)
	}
}

// TestTraceStore_DurationSpansOutOfOrderEvents keeps the fix honest: a real
// multi-second span gap must still be measured.
func TestTraceStore_DurationSpansOutOfOrderEvents(t *testing.T) {
	db := setupTestDB(t)
	ts := NewTraceStore(db)
	ctx := context.Background()

	base := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	offsets := []time.Duration{5 * time.Second, 0, 2 * time.Second}
	for i, offset := range offsets {
		if err := ts.UpsertTraceStatus(ctx, "trace-2", store.LogEntry{
			Level:     "info",
			Service:   "api",
			SpanID:    fmt.Sprintf("span-%d", i),
			Timestamp: base.Add(offset),
		}); err != nil {
			t.Fatalf("UpsertTraceStatus: %v", err)
		}
	}

	status, err := ts.GetTraceStatus(ctx, "trace-2")
	if err != nil {
		t.Fatalf("GetTraceStatus: %v", err)
	}
	if status.DurationMs != 5000 {
		t.Errorf("duration_ms = %v, want 5000 (earliest to latest span)", status.DurationMs)
	}
}

// TestUserStore_ConcurrentDemotesKeepAnAdmin covers the last-admin TOCTOU: the
// check and the write now share a transaction, so two concurrent demotes of the
// final two admins cannot both succeed.
func TestUserStore_ConcurrentDemotesKeepAnAdmin(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	// Repeat: the interleaving that defeats an unsynchronised check is
	// scheduler-dependent, but the invariant below must hold every round.
	const rounds = 20
	for round := 0; round < rounds; round++ {
		a, err := us.Create(ctx, store.CreateUserParams{
			Email: fmt.Sprintf("admin-a-%d@example.com", round), PasswordHash: "h",
			DisplayName: "A", Role: store.RoleAdmin,
		})
		if err != nil {
			t.Fatalf("Create admin A: %v", err)
		}
		b, err := us.Create(ctx, store.CreateUserParams{
			Email: fmt.Sprintf("admin-b-%d@example.com", round), PasswordHash: "h",
			DisplayName: "B", Role: store.RoleAdmin,
		})
		if err != nil {
			t.Fatalf("Create admin B: %v", err)
		}

		member := store.RoleMember
		var wg sync.WaitGroup
		for _, id := range []string{a.ID, b.ID} {
			wg.Add(1)
			go func(userID string) {
				defer wg.Done()
				_, _ = us.Update(ctx, userID, store.UpdateUserParams{Role: &member})
			}(id)
		}
		wg.Wait()

		var admins int
		if err := db.NewRaw(
			`SELECT COUNT(*) FROM users WHERE role = 'admin' AND is_active = 1`,
		).Scan(ctx, &admins); err != nil {
			t.Fatalf("count admins: %v", err)
		}
		if admins == 0 {
			t.Fatalf("round %d: concurrent demotes removed the last admin", round)
		}

		// Clean up for the next round: demote the survivor is impossible, so
		// promote both back and delete them via a direct statement.
		if _, err := db.NewRaw(`DELETE FROM users`).Exec(ctx); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	}
}

// TestGetImpact_ScopedToEnvironment covers the env-scoped read that env-pinned
// MCP callers use. The all-env aggregate names only its highest-impact env, so
// gating on that env denied a caller whose own env simply scored lower — here
// staging outscores production, and a production-scoped read must still return
// production's own numbers rather than staging's or a denial.
func TestGetImpact_ScopedToEnvironment(t *testing.T) {
	db := setupTestDB(t)
	egs := NewErrorGroupStore(db)
	eis := NewErrorImpactStore(db)
	ctx := context.Background()

	// production: one affected user. staging: two, and the higher score.
	seedErrorGroup(t, egs, "fp-scoped", "production")
	seedErrorGroup(t, egs, "fp-scoped", "staging")
	if err := eis.TrackImpact(ctx, "fp-scoped", "production", "prod-user", nil, 1, "api"); err != nil {
		t.Fatalf("TrackImpact production: %v", err)
	}
	for _, u := range []string{"stg-user-1", "stg-user-2"} {
		if err := eis.TrackImpact(ctx, "fp-scoped", "staging", u, nil, 2, "api"); err != nil {
			t.Fatalf("TrackImpact staging: %v", err)
		}
	}
	if _, err := db.NewRaw(
		`UPDATE error_groups SET impact_score = ? WHERE fingerprint = ? AND environment = ?`,
		9.5, "fp-scoped", "staging",
	).Exec(ctx); err != nil {
		t.Fatalf("set staging score: %v", err)
	}

	prod, err := eis.GetImpact(ctx, "fp-scoped", "production")
	if err != nil {
		t.Fatalf("GetImpact production: %v", err)
	}
	if prod.Environment != "production" {
		t.Errorf("Environment = %q, want production — a production-scoped read must not report staging", prod.Environment)
	}
	if prod.UniqueUsers != 1 {
		t.Errorf("production unique_users = %d, want 1 (staging's 2 users must not leak in)", prod.UniqueUsers)
	}

	stg, err := eis.GetImpact(ctx, "fp-scoped", "staging")
	if err != nil {
		t.Fatalf("GetImpact staging: %v", err)
	}
	if stg.UniqueUsers != 2 {
		t.Errorf("staging unique_users = %d, want 2", stg.UniqueUsers)
	}

	// "" keeps the cross-env aggregate.
	all, err := eis.GetImpact(ctx, "fp-scoped", "")
	if err != nil {
		t.Fatalf("GetImpact aggregate: %v", err)
	}
	if all.UniqueUsers != 3 {
		t.Errorf("aggregate unique_users = %d, want 3", all.UniqueUsers)
	}
}
