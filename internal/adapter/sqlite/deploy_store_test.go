package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func setupDeployStore(t *testing.T) store.DeployStore {
	t.Helper()
	db := setupTestDB(t)
	return NewDeployStore(db)
}

func TestDeployCreate(t *testing.T) {
	s := setupDeployStore(t)
	ctx := context.Background()

	d, err := s.Create(ctx, store.CreateDeployParams{
		Service:      "api",
		Environment:  "production",
		CommitHash:   "abc123",
		Branch:       "main",
		Author:       "dev@example.com",
		FilesChanged: []string{"users.rb", "orders.rb"},
		DeploySource: store.DeploySourceWebhook,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.Service != "api" {
		t.Errorf("got service %q", d.Service)
	}
	if d.CommitHash != "abc123" {
		t.Errorf("got commit %q", d.CommitHash)
	}
	if len(d.FilesChanged) != 2 {
		t.Errorf("expected 2 files, got %d", len(d.FilesChanged))
	}
	if d.Status != store.DeployStatusPending {
		t.Errorf("expected pending, got %q", d.Status)
	}
}

func TestDeployGetByID(t *testing.T) {
	s := setupDeployStore(t)
	ctx := context.Background()

	d, err := s.Create(ctx, store.CreateDeployParams{
		Service:    "api",
		CommitHash: "def456",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.CommitHash != "def456" {
		t.Errorf("got %q", got.CommitHash)
	}

	_, err = s.GetByID(ctx, 99999)
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeployGetByCommit(t *testing.T) {
	s := setupDeployStore(t)
	ctx := context.Background()

	_, err := s.Create(ctx, store.CreateDeployParams{
		Service:    "api",
		CommitHash: "xyz789",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	d, err := s.GetByCommit(ctx, "xyz789")
	if err != nil {
		t.Fatalf("get by commit: %v", err)
	}
	if d.CommitHash != "xyz789" {
		t.Errorf("got %q", d.CommitHash)
	}

	_, err = s.GetByCommit(ctx, "missing")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeployGetRecent(t *testing.T) {
	s := setupDeployStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := s.Create(ctx, store.CreateDeployParams{
			Service:    "api",
			CommitHash: string(rune('a' + i)),
		})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	deploys, err := s.GetRecent(ctx, "api", 3)
	if err != nil {
		t.Fatalf("get recent: %v", err)
	}
	if len(deploys) != 3 {
		t.Errorf("expected 3, got %d", len(deploys))
	}

	// No filter
	all, err := s.GetRecent(ctx, "", 10)
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("expected 5, got %d", len(all))
	}
}

func TestDeployMeasureImpact(t *testing.T) {
	s := setupDeployStore(t)
	ctx := context.Background()

	d, err := s.Create(ctx, store.CreateDeployParams{
		Service:    "api",
		CommitHash: "impact1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	err = s.MeasureImpact(ctx, d.ID, store.DeployImpact{
		PreErrorRate:     0.01,
		PostErrorRate:    0.05,
		PreAvgDurationMs: 100,
		PostAvgDurationMs: 150,
		IsIncident:       true,
	})
	if err != nil {
		t.Fatalf("measure: %v", err)
	}

	updated, err := s.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if updated.Status != store.DeployStatusIncident {
		t.Errorf("expected incident, got %q", updated.Status)
	}
	if updated.PreErrorRate == nil || *updated.PreErrorRate != 0.01 {
		t.Error("pre_error_rate not set correctly")
	}
}

func TestDeployLinkInvestigation(t *testing.T) {
	s := setupDeployStore(t)
	ctx := context.Background()

	d, err := s.Create(ctx, store.CreateDeployParams{
		Service:    "api",
		CommitHash: "link1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.LinkInvestigation(ctx, d.ID, "sess-1"); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := s.LinkInvestigation(ctx, d.ID, "sess-1"); err != nil {
		t.Fatalf("link dupe: %v", err)
	}
	if err := s.LinkInvestigation(ctx, d.ID, "sess-2"); err != nil {
		t.Fatalf("link 2: %v", err)
	}

	updated, err := s.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(updated.LinkedInvestigationIDs) != 2 {
		t.Errorf("expected 2 linked, got %d", len(updated.LinkedInvestigationIDs))
	}
}

func TestDeployPrune(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	s := setupDeployStore(t)
	ctx := context.Background()

	_, err := s.Create(ctx, store.CreateDeployParams{
		Service:    "api",
		CommitHash: "old1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Sleep so records are older than the prune cutoff (RFC3339 has second precision)
	time.Sleep(2 * time.Second)
	n, err := s.Prune(ctx, 1*time.Second)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 pruned, got %d", n)
	}

	// Verify nothing remains
	deploys, err := s.GetRecent(ctx, "", 10)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(deploys) != 0 {
		t.Errorf("expected 0 after prune, got %d", len(deploys))
	}
}

func TestDeployGetPendingMeasurement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	s := setupDeployStore(t)
	ctx := context.Background()

	_, err := s.Create(ctx, store.CreateDeployParams{
		Service:    "api",
		CommitHash: "pending1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Sleep so the deploy's deployed_at is in the past (RFC3339 has second precision)
	time.Sleep(2 * time.Second)
	// With 1s duration, the deploy is "old enough"
	pending, err := s.GetPendingMeasurement(ctx, 1*time.Second)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected 1 pending, got %d", len(pending))
	}

	// With very large duration, nothing qualifies
	pending2, err := s.GetPendingMeasurement(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("get pending 2: %v", err)
	}
	if len(pending2) != 0 {
		t.Errorf("expected 0 pending, got %d", len(pending2))
	}
}
