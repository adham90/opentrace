package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func setupCodeEntityStore(t *testing.T) CodeEntityStore {
	t.Helper()
	db := setupTestDB(t)
	return NewCodeEntityStore(db)
}

func TestCodeEntityUpsert(t *testing.T) {
	s := setupCodeEntityStore(t)
	ctx := context.Background()

	e, err := s.Upsert(ctx, UpsertCodeEntityParams{
		EntityType: CodeEntityFile,
		EntityName: "app/controllers/users_controller.rb",
		Service:    "api",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if e.EntityName != "app/controllers/users_controller.rb" {
		t.Errorf("got name %q", e.EntityName)
	}
	if e.Service != "api" {
		t.Errorf("got service %q", e.Service)
	}

	// Upsert again should not error
	e2, err := s.Upsert(ctx, UpsertCodeEntityParams{
		EntityType: CodeEntityFile,
		EntityName: "app/controllers/users_controller.rb",
		Service:    "api",
	})
	if err != nil {
		t.Fatalf("upsert again: %v", err)
	}
	if e2.ID != e.ID {
		t.Errorf("expected same ID %d, got %d", e.ID, e2.ID)
	}
}

func TestCodeEntityGetByName(t *testing.T) {
	s := setupCodeEntityStore(t)
	ctx := context.Background()

	_, err := s.Upsert(ctx, UpsertCodeEntityParams{
		EntityType: CodeEntityController,
		EntityName: "UsersController",
		Service:    "web",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	e, err := s.GetByName(ctx, CodeEntityController, "UsersController", "web")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if e.EntityName != "UsersController" {
		t.Errorf("got %q", e.EntityName)
	}

	// Not found
	_, err = s.GetByName(ctx, CodeEntityController, "Missing", "web")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCodeEntityIncrementError(t *testing.T) {
	s := setupCodeEntityStore(t)
	ctx := context.Background()

	if err := s.IncrementError(ctx, CodeEntityFile, "users.rb", "api"); err != nil {
		t.Fatalf("increment: %v", err)
	}
	if err := s.IncrementError(ctx, CodeEntityFile, "users.rb", "api"); err != nil {
		t.Fatalf("increment 2: %v", err)
	}

	e, err := s.GetByName(ctx, CodeEntityFile, "users.rb", "api")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if e.ErrorCount != 2 {
		t.Errorf("expected error_count=2, got %d", e.ErrorCount)
	}
	if e.LastErrorAt == nil {
		t.Error("expected last_error_at to be set")
	}
}

func TestCodeEntityIncrementInvestigation(t *testing.T) {
	s := setupCodeEntityStore(t)
	ctx := context.Background()

	if err := s.IncrementInvestigation(ctx, CodeEntityFile, "orders.rb", "api"); err != nil {
		t.Fatalf("increment: %v", err)
	}

	e, err := s.GetByName(ctx, CodeEntityFile, "orders.rb", "api")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if e.InvestigationCount != 1 {
		t.Errorf("expected investigation_count=1, got %d", e.InvestigationCount)
	}
}

func TestCodeEntityTopByRisk(t *testing.T) {
	s := setupCodeEntityStore(t)
	ctx := context.Background()

	// Create entities with different error counts
	for i := 0; i < 5; i++ {
		for j := 0; j <= i; j++ {
			if err := s.IncrementError(ctx, CodeEntityFile, fmt.Sprintf("file%d.rb", i), "api"); err != nil {
				t.Fatalf("increment: %v", err)
			}
		}
	}

	// Recompute risk
	if err := s.BatchRecomputeRisk(ctx); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	entities, err := s.TopByRisk(ctx, "api", 3)
	if err != nil {
		t.Fatalf("top by risk: %v", err)
	}
	if len(entities) != 3 {
		t.Fatalf("expected 3, got %d", len(entities))
	}
	// Highest error count should have highest risk
	if entities[0].ErrorCount < entities[1].ErrorCount {
		t.Errorf("expected descending risk order")
	}
}

func TestCodeEntityBatchGetRisk(t *testing.T) {
	s := setupCodeEntityStore(t)
	ctx := context.Background()

	if err := s.IncrementError(ctx, CodeEntityFile, "a.rb", "api"); err != nil {
		t.Fatalf("increment: %v", err)
	}
	if err := s.IncrementError(ctx, CodeEntityFile, "b.rb", "api"); err != nil {
		t.Fatalf("increment: %v", err)
	}

	entities, err := s.BatchGetRisk(ctx, CodeEntityFile, []string{"a.rb", "b.rb", "missing.rb"}, "api")
	if err != nil {
		t.Fatalf("batch get: %v", err)
	}
	if len(entities) != 2 {
		t.Errorf("expected 2, got %d", len(entities))
	}
}

func TestCodeEntityPrune(t *testing.T) {
	s := setupCodeEntityStore(t)
	ctx := context.Background()

	// Create entity with no errors (pruneable)
	_, err := s.Upsert(ctx, UpsertCodeEntityParams{
		EntityType: CodeEntityFile,
		EntityName: "old.rb",
		Service:    "api",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Create entity with errors (not pruneable)
	if err := s.IncrementError(ctx, CodeEntityFile, "active.rb", "api"); err != nil {
		t.Fatalf("increment: %v", err)
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
}
