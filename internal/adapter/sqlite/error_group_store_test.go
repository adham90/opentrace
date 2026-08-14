package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestErrorGroupStore_Upsert_NewGroup(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	entry := store.LogEntry{
		ID:               0,
		Level:            "ERROR",
		Service:          "myapp",
		Environment:      "production",
		ExceptionClass:   "Redis::TimeoutError",
		Message:          "Connection timed out",
		ErrorFingerprint: "fp_abc123",
		SourceFile:       "app/models/user.rb",
		SourceLine:       42,
		Timestamp:        time.Now(),
	}

	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	eg, err := s.Get(ctx, "fp_abc123", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if eg.Service != "myapp" {
		t.Errorf("Service = %q, want myapp", eg.Service)
	}
	if eg.ExceptionClass != "Redis::TimeoutError" {
		t.Errorf("ExceptionClass = %q, want Redis::TimeoutError", eg.ExceptionClass)
	}
	if eg.OccurrenceCount != 1 {
		t.Errorf("OccurrenceCount = %d, want 1", eg.OccurrenceCount)
	}
	if eg.Status != store.ErrorGroupUnresolved {
		t.Errorf("Status = %q, want unresolved", eg.Status)
	}
}

func TestErrorGroupStore_Upsert_IncrementExisting(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	entry := store.LogEntry{
		ID: 0, Level: "ERROR", Service: "myapp",
		ErrorFingerprint: "fp_inc", Timestamp: time.Now(),
	}
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	eg, err := s.Get(ctx, "fp_inc", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if eg.OccurrenceCount != 2 {
		t.Errorf("OccurrenceCount = %d, want 2", eg.OccurrenceCount)
	}
}

func TestErrorGroupStore_Upsert_SkipsNonError(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	// INFO level should be skipped.
	entry := store.LogEntry{
		ID: 0, Level: "INFO", Service: "myapp",
		ErrorFingerprint: "fp_info", Timestamp: time.Now(),
	}
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	_, err := s.Get(ctx, "fp_info", "")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound for INFO-level entry, got: %v", err)
	}
}

func TestErrorGroupStore_Upsert_SkipsEmptyFingerprint(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	entry := store.LogEntry{ID: 0, Level: "ERROR", Service: "myapp", Timestamp: time.Now()}
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// No error group should be created.
	count, err := s.Count(ctx, "", "")
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestErrorGroupStore_Resolve_And_Reopen(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	entry := store.LogEntry{
		ID: 0, Level: "ERROR", Service: "myapp",
		ErrorFingerprint: "fp_resolve", Timestamp: time.Now(),
	}
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Resolve.
	if err := s.Resolve(ctx, "fp_resolve", "", "Fixed in PR #42"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	eg, _ := s.Get(ctx, "fp_resolve", "")
	if eg.Status != store.ErrorGroupResolved {
		t.Errorf("Status = %q, want resolved", eg.Status)
	}
	if eg.ResolvedAt == nil {
		t.Error("ResolvedAt should be set")
	}

	// Reopen via new occurrence.
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert reopen: %v", err)
	}
	eg, _ = s.Get(ctx, "fp_resolve", "")
	if eg.Status != store.ErrorGroupUnresolved {
		t.Errorf("Status = %q, want unresolved after reopen", eg.Status)
	}
	if eg.ReopenedCount != 1 {
		t.Errorf("ReopenedCount = %d, want 1", eg.ReopenedCount)
	}
	if eg.OccurrenceCount != 2 {
		t.Errorf("OccurrenceCount = %d, want 2", eg.OccurrenceCount)
	}

	// Verify events.
	events, err := s.ListEvents(ctx, "fp_resolve", "", 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	// Both have the same second-level timestamp; verify both actions present.
	actions := map[string]bool{}
	for _, ev := range events {
		actions[ev.Action] = true
	}
	if !actions["resolved"] {
		t.Error("expected 'resolved' event")
	}
	if !actions["reopened"] {
		t.Error("expected 'reopened' event")
	}
}

func TestErrorGroupStore_Ignore_StaysIgnored(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	entry := store.LogEntry{
		ID: 0, Level: "ERROR", Service: "myapp",
		ErrorFingerprint: "fp_ignore", Timestamp: time.Now(),
	}
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.Ignore(ctx, "fp_ignore", "", "Known noise"); err != nil {
		t.Fatalf("Ignore: %v", err)
	}

	// New occurrence should NOT reopen an ignored error.
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert after ignore: %v", err)
	}
	eg, _ := s.Get(ctx, "fp_ignore", "")
	if eg.Status != store.ErrorGroupIgnored {
		t.Errorf("Status = %q, want ignored", eg.Status)
	}
	// Occurrence count still increments.
	if eg.OccurrenceCount != 2 {
		t.Errorf("OccurrenceCount = %d, want 2", eg.OccurrenceCount)
	}
}

func TestErrorGroupStore_List(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	for _, fp := range []string{"fp_a", "fp_b", "fp_c"} {
		entry := store.LogEntry{
			Level: "ERROR", Service: "myapp",
			ErrorFingerprint: fp, Timestamp: time.Now(),
		}
		if err := s.Upsert(ctx, entry); err != nil {
			t.Fatalf("Upsert %s: %v", fp, err)
		}
	}

	groups, err := s.List(ctx, store.ListErrorGroupParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(groups) != 3 {
		t.Errorf("len(groups) = %d, want 3", len(groups))
	}

	// Filter by status.
	if err := s.Resolve(ctx, "fp_b", "", "fixed"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	unresolved, err := s.List(ctx, store.ListErrorGroupParams{Status: store.ErrorGroupUnresolved})
	if err != nil {
		t.Fatalf("List unresolved: %v", err)
	}
	if len(unresolved) != 2 {
		t.Errorf("unresolved count = %d, want 2", len(unresolved))
	}
}

func TestErrorGroupStore_Count(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	for _, fp := range []string{"fp_1", "fp_2", "fp_3"} {
		entry := store.LogEntry{
			Level: "FATAL", Service: "myapp",
			ErrorFingerprint: fp, Timestamp: time.Now(),
		}
		if err := s.Upsert(ctx, entry); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	total, err := s.Count(ctx, "", "")
	if err != nil {
		t.Fatalf("Count all: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}

	unresolved, err := s.Count(ctx, store.ErrorGroupUnresolved, "")
	if err != nil {
		t.Fatalf("Count unresolved: %v", err)
	}
	if unresolved != 3 {
		t.Errorf("unresolved = %d, want 3", unresolved)
	}
}

func TestErrorGroupStore_Resolve_NotFound(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	err := s.Resolve(ctx, "nonexistent", "", "reason")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestErrorGroupStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	old := store.LogEntry{
		ID: 0, Level: "ERROR", Service: "myapp",
		ErrorFingerprint: "fp_old", Timestamp: time.Now().Add(-48 * time.Hour),
	}
	if err := s.Upsert(ctx, old); err != nil {
		t.Fatalf("Upsert old: %v", err)
	}

	recent := store.LogEntry{
		Level: "ERROR", Service: "myapp",
		ErrorFingerprint: "fp_recent", Timestamp: time.Now(),
	}
	if err := s.Upsert(ctx, recent); err != nil {
		t.Fatalf("Upsert recent: %v", err)
	}

	pruned, err := s.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want 1", pruned)
	}

	remaining, _ := s.Count(ctx, "", "")
	if remaining != 1 {
		t.Errorf("remaining = %d, want 1", remaining)
	}
}

func TestErrorGroupStore_CompositePKAndSeenInEnvs(t *testing.T) {
	db := setupTestDB(t)
	egs := NewErrorGroupStore(db)
	ctx := context.Background()

	// Upsert in staging.
	err := egs.Upsert(ctx, store.LogEntry{
		ID:               1,
		Level:            "error",
		Timestamp:        time.Now().UTC(),
		ErrorFingerprint: "fp-crash-1",
		Service:          "api",
		Environment:      "staging",
		ExceptionClass:   "NullPointerException",
		Message:          "boom",
		SourceFile:       "foo.rb",
		SourceLine:       42,
	})
	if err != nil {
		t.Fatalf("Upsert staging: %v", err)
	}

	// Upsert the same fingerprint in production — should create a second row.
	err = egs.Upsert(ctx, store.LogEntry{
		ID:               2,
		Level:            "error",
		Timestamp:        time.Now().UTC(),
		ErrorFingerprint: "fp-crash-1",
		Service:          "api",
		Environment:      "production",
		ExceptionClass:   "NullPointerException",
		Message:          "boom",
		SourceFile:       "foo.rb",
		SourceLine:       42,
	})
	if err != nil {
		t.Fatalf("Upsert production: %v", err)
	}

	// List should return both rows.
	groups, err := egs.List(ctx, store.ListErrorGroupParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("List = %d rows, want 2 (one per env)", len(groups))
	}

	// Each row should carry seen_in_envs = both envs.
	for _, g := range groups {
		if len(g.SeenInEnvs) != 2 {
			t.Errorf("group env=%q SeenInEnvs = %v, want both envs", g.Environment, g.SeenInEnvs)
		}
		hasStaging := false
		hasProd := false
		for _, e := range g.SeenInEnvs {
			switch e {
			case "staging":
				hasStaging = true
			case "production":
				hasProd = true
			}
		}
		if !hasStaging || !hasProd {
			t.Errorf("group env=%q SeenInEnvs = %v, want both staging and production", g.Environment, g.SeenInEnvs)
		}
	}

	// Filter by env returns only the matching row.
	stagingOnly, err := egs.List(ctx, store.ListErrorGroupParams{Environment: "staging"})
	if err != nil {
		t.Fatalf("List staging: %v", err)
	}
	if len(stagingOnly) != 1 || stagingOnly[0].Environment != "staging" {
		t.Errorf("List staging = %+v", stagingOnly)
	}
}

func TestErrorGroupStore_ResolveScopedToEnv(t *testing.T) {
	db := setupTestDB(t)
	egs := NewErrorGroupStore(db)
	ctx := context.Background()

	// Seed the same fingerprint in two envs.
	for _, env := range []string{"staging", "production"} {
		if err := egs.Upsert(ctx, store.LogEntry{
			ID:               int64(len(env)),
			Level:            "error",
			Timestamp:        time.Now().UTC(),
			ErrorFingerprint: "fp-2env",
			Service:          "api",
			Environment:      env,
			ExceptionClass:   "ArgError",
		}); err != nil {
			t.Fatalf("Upsert %s: %v", env, err)
		}
	}

	// Resolve only staging.
	if err := egs.Resolve(ctx, "fp-2env", "staging", "fixed in staging first"); err != nil {
		t.Fatalf("Resolve(staging): %v", err)
	}

	// Staging row is resolved; production stays unresolved.
	groups, err := egs.List(ctx, store.ListErrorGroupParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, g := range groups {
		switch g.Environment {
		case "staging":
			if g.Status != store.ErrorGroupResolved {
				t.Errorf("staging row status = %q, want resolved", g.Status)
			}
		case "production":
			if g.Status != store.ErrorGroupUnresolved {
				t.Errorf("production row status = %q, want unresolved", g.Status)
			}
		}
	}

	// Blanket Resolve closes both.
	if err := egs.Resolve(ctx, "fp-2env", "", "fixed everywhere"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	groups, _ = egs.List(ctx, store.ListErrorGroupParams{})
	for _, g := range groups {
		if g.Status != store.ErrorGroupResolved {
			t.Errorf("after blanket resolve, env=%q status = %q, want resolved", g.Environment, g.Status)
		}
	}
}

func TestErrorImpactStore_EnvIsolation(t *testing.T) {
	db := setupTestDB(t)
	egs := NewErrorGroupStore(db)
	eis := NewErrorImpactStore(db)
	ctx := context.Background()

	// Seed parent groups in both envs.
	for _, env := range []string{"staging", "production"} {
		if err := egs.Upsert(ctx, store.LogEntry{
			ID:               1,
			Level:            "error",
			Timestamp:        time.Now().UTC(),
			ErrorFingerprint: "fp-impact",
			Service:          "api",
			Environment:      env,
			ExceptionClass:   "X",
		}); err != nil {
			t.Fatalf("Upsert %s: %v", env, err)
		}
	}

	// Same user hits the error in both envs — should create two impact rows.
	if err := eis.TrackImpact(ctx, "fp-impact", "staging", "user-1", nil, 1, "api"); err != nil {
		t.Fatalf("TrackImpact staging: %v", err)
	}
	if err := eis.TrackImpact(ctx, "fp-impact", "production", "user-1", nil, 2, "api"); err != nil {
		t.Fatalf("TrackImpact production: %v", err)
	}

	// GetImpact aggregates across both envs: 2 unique "user rows", even though
	// it's the same user in two envs, because the unique constraint is
	// (fingerprint, environment, user_id).
	impact, err := eis.GetImpact(ctx, "fp-impact", "")
	if err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if impact.UniqueUsers != 2 {
		t.Errorf("GetImpact.UniqueUsers = %d, want 2 (one row per env)", impact.UniqueUsers)
	}
}
