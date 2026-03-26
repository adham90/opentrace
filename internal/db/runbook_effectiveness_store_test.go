package db

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/pkg/store"
)

func TestRunbookEffectivenessStore_RecordAndList(t *testing.T) {
	db := setupTestDB(t)
	s := NewRunbookEffectivenessStore(db)
	ctx := context.Background()

	// Record several executions
	for i := 0; i < 4; i++ {
		if err := s.RecordExecution(ctx, "slow_query_playbook"); err != nil {
			t.Fatalf("record execution %d: %v", i, err)
		}
	}
	if err := s.RecordExecution(ctx, "error_spike_playbook"); err != nil {
		t.Fatal(err)
	}

	// List should return both, ordered by total_executions desc
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
	if list[0].RunbookName != "slow_query_playbook" {
		t.Errorf("first = %q, want slow_query_playbook", list[0].RunbookName)
	}
	if list[0].TotalExecutions != 4 {
		t.Errorf("total_executions = %d, want 4", list[0].TotalExecutions)
	}
	if list[1].RunbookName != "error_spike_playbook" {
		t.Errorf("second = %q, want error_spike_playbook", list[1].RunbookName)
	}
}

func TestRunbookEffectivenessStore_UpdateOutcome(t *testing.T) {
	db := setupTestDB(t)
	s := NewRunbookEffectivenessStore(db)
	ctx := context.Background()

	// Record executions
	for i := 0; i < 5; i++ {
		if err := s.RecordExecution(ctx, "my_playbook"); err != nil {
			t.Fatal(err)
		}
	}

	// Update outcomes: 3 resolved, 1 abandoned
	for i := 0; i < 3; i++ {
		err := s.UpdateOutcome(ctx, store.UpdateRunbookEffectivenessParams{
			RunbookName: "my_playbook",
			Outcome:     "resolved",
			StepsAfter:  5,
			DurationSec: 120,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	err := s.UpdateOutcome(ctx, store.UpdateRunbookEffectivenessParams{
		RunbookName: "my_playbook",
		Outcome:     "abandoned",
		StepsAfter:  10,
		DurationSec: 300,
	})
	if err != nil {
		t.Fatal(err)
	}

	// List and verify
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d", len(list))
	}
	re := list[0]
	if re.ResolvedSessions != 3 {
		t.Errorf("resolved = %d, want 3", re.ResolvedSessions)
	}
	if re.AbandonedSessions != 1 {
		t.Errorf("abandoned = %d, want 1", re.AbandonedSessions)
	}
}

func TestRunbookEffectivenessStore_GetMostEffective(t *testing.T) {
	db := setupTestDB(t)
	s := NewRunbookEffectivenessStore(db)
	ctx := context.Background()

	// No entries — should return nil
	best, err := s.GetMostEffective(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if best != nil {
		t.Error("expected nil when no entries")
	}

	// Create a playbook with <3 executions — should not qualify
	for i := 0; i < 2; i++ {
		s.RecordExecution(ctx, "too_few")
	}
	s.UpdateOutcome(ctx, store.UpdateRunbookEffectivenessParams{
		RunbookName: "too_few", Outcome: "resolved", StepsAfter: 1, DurationSec: 60,
	})

	best, err = s.GetMostEffective(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if best != nil {
		t.Error("expected nil when no playbook has >= 3 executions")
	}

	// Create a playbook with 4 executions, 3 resolved — should qualify
	for i := 0; i < 4; i++ {
		s.RecordExecution(ctx, "effective")
	}
	for i := 0; i < 3; i++ {
		s.UpdateOutcome(ctx, store.UpdateRunbookEffectivenessParams{
			RunbookName: "effective", Outcome: "resolved", StepsAfter: 5, DurationSec: 100,
		})
	}

	best, err = s.GetMostEffective(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if best == nil {
		t.Fatal("expected non-nil best")
	}
	if best.RunbookName != "effective" {
		t.Errorf("runbook_name = %q, want effective", best.RunbookName)
	}
	if best.TotalExecutions != 4 {
		t.Errorf("total_executions = %d, want 4", best.TotalExecutions)
	}
	if best.ResolvedSessions != 3 {
		t.Errorf("resolved = %d, want 3", best.ResolvedSessions)
	}
}

func TestRunbookEffectivenessStore_GetMostEffective_RequiresPositiveResolution(t *testing.T) {
	db := setupTestDB(t)
	s := NewRunbookEffectivenessStore(db)
	ctx := context.Background()

	// Create a playbook with more abandoned than resolved
	for i := 0; i < 4; i++ {
		s.RecordExecution(ctx, "bad_playbook")
	}
	s.UpdateOutcome(ctx, store.UpdateRunbookEffectivenessParams{
		RunbookName: "bad_playbook", Outcome: "resolved", StepsAfter: 5, DurationSec: 100,
	})
	for i := 0; i < 3; i++ {
		s.UpdateOutcome(ctx, store.UpdateRunbookEffectivenessParams{
			RunbookName: "bad_playbook", Outcome: "abandoned", StepsAfter: 10, DurationSec: 300,
		})
	}

	best, err := s.GetMostEffective(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if best != nil {
		t.Error("expected nil when abandoned > resolved")
	}
}
