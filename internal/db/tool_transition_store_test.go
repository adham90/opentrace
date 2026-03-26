package db

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestToolTransitionStore_IncrementAndQuery(t *testing.T) {
	db := setupTestDB(t)
	s := NewToolTransitionStore(db)
	ctx := context.Background()

	// Increment a transition multiple times
	for i := 0; i < 5; i++ {
		if err := s.Increment(ctx, "diagnose", "error_groups", "investigation"); err != nil {
			t.Fatalf("increment: %v", err)
		}
	}

	transitions, err := s.GetTransitions(ctx, store.GetTransitionsParams{
		FromTool:   "diagnose",
		Intent:     "investigation",
		MinSupport: 1,
		MaxAgeDays: 90,
	})
	if err != nil {
		t.Fatalf("get transitions: %v", err)
	}
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].TotalCount != 5 {
		t.Errorf("expected total_count=5, got %d", transitions[0].TotalCount)
	}
	if transitions[0].ToTool != "error_groups" {
		t.Errorf("expected to_tool=error_groups, got %q", transitions[0].ToTool)
	}
}

func TestToolTransitionStore_IncrementWithOutcome(t *testing.T) {
	db := setupTestDB(t)
	s := NewToolTransitionStore(db)
	ctx := context.Background()

	// Record transitions with outcomes
	for i := 0; i < 3; i++ {
		if err := s.IncrementWithOutcome(ctx, "diagnose", "log_search", "investigation", "resolved"); err != nil {
			t.Fatalf("increment with outcome: %v", err)
		}
	}
	if err := s.IncrementWithOutcome(ctx, "diagnose", "log_search", "investigation", "abandoned"); err != nil {
		t.Fatalf("increment with outcome: %v", err)
	}

	transitions, err := s.GetTransitions(ctx, store.GetTransitionsParams{
		FromTool: "diagnose",
		Intent:   "investigation",
	})
	if err != nil {
		t.Fatalf("get transitions: %v", err)
	}
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].ResolvedCount != 3 {
		t.Errorf("expected resolved_count=3, got %d", transitions[0].ResolvedCount)
	}
	if transitions[0].AbandonedCount != 1 {
		t.Errorf("expected abandoned_count=1, got %d", transitions[0].AbandonedCount)
	}
	if transitions[0].TotalCount != 4 {
		t.Errorf("expected total_count=4, got %d", transitions[0].TotalCount)
	}
}

func TestToolTransitionStore_GetDeadEnds(t *testing.T) {
	db := setupTestDB(t)
	s := NewToolTransitionStore(db)
	ctx := context.Background()

	// Create a dead-end: more abandoned than resolved
	for i := 0; i < 5; i++ {
		if err := s.IncrementWithOutcome(ctx, "log_search", "trace_lookup", "investigation", "abandoned"); err != nil {
			t.Fatalf("increment: %v", err)
		}
	}
	// Only 1 resolved
	if err := s.IncrementWithOutcome(ctx, "log_search", "trace_lookup", "investigation", "resolved"); err != nil {
		t.Fatalf("increment: %v", err)
	}

	deadEnds, err := s.GetDeadEnds(ctx, "investigation")
	if err != nil {
		t.Fatalf("get dead ends: %v", err)
	}
	if len(deadEnds) != 1 {
		t.Fatalf("expected 1 dead end, got %d", len(deadEnds))
	}
	if deadEnds[0].FromTool != "log_search" || deadEnds[0].ToTool != "trace_lookup" {
		t.Errorf("unexpected dead end: %s → %s", deadEnds[0].FromTool, deadEnds[0].ToTool)
	}
}

func TestToolTransitionStore_MinSupportFilter(t *testing.T) {
	db := setupTestDB(t)
	s := NewToolTransitionStore(db)
	ctx := context.Background()

	// Only 2 counts — below minSupport of 3
	for i := 0; i < 2; i++ {
		if err := s.Increment(ctx, "diagnose", "error_groups", "investigation"); err != nil {
			t.Fatalf("increment: %v", err)
		}
	}

	transitions, err := s.GetTransitions(ctx, store.GetTransitionsParams{
		FromTool:   "diagnose",
		Intent:     "investigation",
		MinSupport: 3,
	})
	if err != nil {
		t.Fatalf("get transitions: %v", err)
	}
	if len(transitions) != 0 {
		t.Errorf("expected 0 transitions with minSupport=3, got %d", len(transitions))
	}
}

func TestToolTransitionStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	s := NewToolTransitionStore(db)
	ctx := context.Background()

	if err := s.Increment(ctx, "a", "b", ""); err != nil {
		t.Fatalf("increment: %v", err)
	}

	// Prune with 0 duration should delete everything
	n, err := s.Prune(ctx, 0*time.Second)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 pruned, got %d", n)
	}
}
