package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestDeployStoreRecordIsIdempotent(t *testing.T) {
	db := setupTestDB(t)
	s := NewDeployStore(db)
	ctx := context.Background()

	d := store.Deploy{CommitHash: "abc123", Service: "api", Environment: "production", FirstSeenAt: time.Now()}
	for i := 0; i < 3; i++ {
		if err := s.Record(ctx, d); err != nil {
			t.Fatalf("Record #%d: %v", i, err)
		}
	}

	list, err := s.List(ctx, store.ListDeployParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("recording the same commit 3× produced %d rows, want 1", len(list))
	}
}

// The first sighting is the deploy time. A later batch carrying the same commit
// must not move it forward, or "since last_deploy" would keep sliding.
func TestDeployStoreRecordKeepsFirstSighting(t *testing.T) {
	db := setupTestDB(t)
	s := NewDeployStore(db)
	ctx := context.Background()

	first := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	if err := s.Record(ctx, store.Deploy{CommitHash: "abc", Service: "api", Environment: "production", FirstSeenAt: first}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Record(ctx, store.Deploy{CommitHash: "abc", Service: "api", Environment: "production", FirstSeenAt: time.Now()}); err != nil {
		t.Fatalf("Record again: %v", err)
	}

	got, err := s.Latest(ctx, "api", "production")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !got.FirstSeenAt.Equal(first.UTC()) {
		t.Errorf("FirstSeenAt = %v, want the original sighting %v", got.FirstSeenAt, first.UTC())
	}
}

// The same commit in two environments is two deploys — staging going out first
// must not make production look already-deployed.
func TestDeployStoreSeparatesEnvironments(t *testing.T) {
	db := setupTestDB(t)
	s := NewDeployStore(db)
	ctx := context.Background()

	now := time.Now()
	mustRecord(t, s, store.Deploy{CommitHash: "abc", Service: "api", Environment: "staging", FirstSeenAt: now.Add(-1 * time.Hour)})
	mustRecord(t, s, store.Deploy{CommitHash: "abc", Service: "api", Environment: "production", FirstSeenAt: now})

	all, err := s.List(ctx, store.ListDeployParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d deploys, want 2 (one per env)", len(all))
	}

	prod, err := s.Latest(ctx, "api", "production")
	if err != nil {
		t.Fatalf("Latest(production): %v", err)
	}
	if prod.Environment != "production" {
		t.Errorf("Latest(production).Environment = %q", prod.Environment)
	}
}

func TestDeployStoreLatestOrdersByTime(t *testing.T) {
	db := setupTestDB(t)
	s := NewDeployStore(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	mustRecord(t, s, store.Deploy{CommitHash: "old", Service: "api", Environment: "production", FirstSeenAt: now.Add(-3 * time.Hour)})
	mustRecord(t, s, store.Deploy{CommitHash: "new", Service: "api", Environment: "production", FirstSeenAt: now.Add(-1 * time.Hour)})
	mustRecord(t, s, store.Deploy{CommitHash: "mid", Service: "api", Environment: "production", FirstSeenAt: now.Add(-2 * time.Hour)})

	got, err := s.Latest(ctx, "", "production")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.CommitHash != "new" {
		t.Errorf("Latest = %q, want %q", got.CommitHash, "new")
	}
}

func TestDeployStoreLatestNotFound(t *testing.T) {
	db := setupTestDB(t)
	s := NewDeployStore(db)

	_, err := s.Latest(context.Background(), "api", "production")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want store.ErrNotFound", err)
	}
}

// An empty commit hash is not a deploy — the SDK omits version for clients that
// do not report one, and recording those would create a phantom marker.
func TestDeployStoreIgnoresEmptyCommit(t *testing.T) {
	db := setupTestDB(t)
	s := NewDeployStore(db)
	ctx := context.Background()

	if err := s.Record(ctx, store.Deploy{Service: "api", Environment: "production", FirstSeenAt: time.Now()}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	list, err := s.List(ctx, store.ListDeployParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("empty commit produced %d rows, want 0", len(list))
	}
}

func TestDeployStoreListCapsLimit(t *testing.T) {
	db := setupTestDB(t)
	s := NewDeployStore(db)
	ctx := context.Background()

	now := time.Now()
	for i := 0; i < 5; i++ {
		mustRecord(t, s, store.Deploy{
			CommitHash:  string(rune('a' + i)),
			Service:     "api",
			Environment: "production",
			FirstSeenAt: now.Add(-time.Duration(i) * time.Hour),
		})
	}

	list, err := s.List(ctx, store.ListDeployParams{Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}

	// An absurd limit is clamped, not honoured.
	big, err := s.List(ctx, store.ListDeployParams{Limit: 100000})
	if err != nil {
		t.Fatalf("List(big): %v", err)
	}
	if len(big) != 5 {
		t.Fatalf("len = %d, want 5", len(big))
	}
}

func TestDeployStorePrune(t *testing.T) {
	db := setupTestDB(t)
	s := NewDeployStore(db)
	ctx := context.Background()

	now := time.Now()
	mustRecord(t, s, store.Deploy{CommitHash: "old", Service: "api", Environment: "production", FirstSeenAt: now.Add(-72 * time.Hour)})
	mustRecord(t, s, store.Deploy{CommitHash: "new", Service: "api", Environment: "production", FirstSeenAt: now})

	n, err := s.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}

	list, err := s.List(ctx, store.ListDeployParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].CommitHash != "new" {
		t.Errorf("remaining = %+v, want only the recent deploy", list)
	}
}

func mustRecord(t *testing.T, s store.DeployStore, d store.Deploy) {
	t.Helper()
	if err := s.Record(context.Background(), d); err != nil {
		t.Fatalf("Record(%s): %v", d.CommitHash, err)
	}
}
