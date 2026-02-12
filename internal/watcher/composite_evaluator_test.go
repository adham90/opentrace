package watcher

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

type mockRunStoreForComposite struct {
	runs map[uuid.UUID][]store.WatcherRun
}

func (m *mockRunStoreForComposite) List(_ context.Context, watcherID uuid.UUID, _ int) ([]store.WatcherRun, error) {
	return m.runs[watcherID], nil
}

func (m *mockRunStoreForComposite) Create(_ context.Context, _ uuid.UUID) (*store.WatcherRun, error) { return nil, nil }
func (m *mockRunStoreForComposite) Complete(_ context.Context, _ uuid.UUID, _ string, _ any, _ bool) error { return nil }
func (m *mockRunStoreForComposite) Fail(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (m *mockRunStoreForComposite) FailStaleRuns(_ context.Context, _ time.Duration) (int, error) { return 0, nil }
func (m *mockRunStoreForComposite) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }
func (m *mockRunStoreForComposite) CountRuns(_ context.Context, _ store.CountRunParams) (int, error) { return 0, nil }
func (m *mockRunStoreForComposite) ListRecentFailed(_ context.Context, _ int) ([]store.WatcherRun, error) { return nil, nil }
func (m *mockRunStoreForComposite) ListWithFilter(_ context.Context, _ uuid.UUID, _ int, _ string) ([]store.WatcherRun, error) { return nil, nil }
func (m *mockRunStoreForComposite) GetByID(_ context.Context, _ uuid.UUID) (*store.WatcherRun, error) { return nil, store.ErrNotFound }

func makeCompositeWatcher(cfg CompositeConfig) store.Watcher {
	tc, _ := json.Marshal(cfg)
	return store.Watcher{
		ID:          uuid.New(),
		Title:       "Composite Test",
		WatcherType: store.WatcherTypeComposite,
		TypeConfig:  tc,
	}
}

func TestComposite_AllOfAllTrue(t *testing.T) {
	w1, w2 := uuid.New(), uuid.New()
	sum1, sum2 := "alert 1", "alert 2"

	rs := &mockRunStoreForComposite{
		runs: map[uuid.UUID][]store.WatcherRun{
			w1: {{Status: "completed", HasAlert: true, Summary: &sum1}},
			w2: {{Status: "completed", HasAlert: true, Summary: &sum2}},
		},
	}
	ce := NewCompositeEvaluator(nil, rs, nil)

	w := makeCompositeWatcher(CompositeConfig{
		Logic: "all_of",
		Conditions: []CompositeCondition{
			{WatcherID: w1.String(), Label: "check_1"},
			{WatcherID: w2.String(), Label: "check_2"},
		},
	})

	result, err := ce.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasAlert {
		t.Error("expected alert when all conditions are true in all_of mode")
	}
}

func TestComposite_AllOfOneFalse(t *testing.T) {
	w1, w2 := uuid.New(), uuid.New()
	sum1, sum2 := "alert 1", "ok"

	rs := &mockRunStoreForComposite{
		runs: map[uuid.UUID][]store.WatcherRun{
			w1: {{Status: "completed", HasAlert: true, Summary: &sum1}},
			w2: {{Status: "completed", HasAlert: false, Summary: &sum2}},
		},
	}
	ce := NewCompositeEvaluator(nil, rs, nil)

	w := makeCompositeWatcher(CompositeConfig{
		Logic: "all_of",
		Conditions: []CompositeCondition{
			{WatcherID: w1.String()},
			{WatcherID: w2.String()},
		},
	})

	result, err := ce.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAlert {
		t.Error("expected no alert when not all conditions are true in all_of mode")
	}
}

func TestComposite_AnyOfOneTrue(t *testing.T) {
	w1, w2 := uuid.New(), uuid.New()
	sum1, sum2 := "alert", "ok"

	rs := &mockRunStoreForComposite{
		runs: map[uuid.UUID][]store.WatcherRun{
			w1: {{Status: "completed", HasAlert: true, Summary: &sum1}},
			w2: {{Status: "completed", HasAlert: false, Summary: &sum2}},
		},
	}
	ce := NewCompositeEvaluator(nil, rs, nil)

	w := makeCompositeWatcher(CompositeConfig{
		Logic: "any_of",
		Conditions: []CompositeCondition{
			{WatcherID: w1.String()},
			{WatcherID: w2.String()},
		},
	})

	result, err := ce.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasAlert {
		t.Error("expected alert when at least one condition is true in any_of mode")
	}
}

func TestComposite_AnyOfAllFalse(t *testing.T) {
	w1, w2 := uuid.New(), uuid.New()
	sum1, sum2 := "ok", "ok"

	rs := &mockRunStoreForComposite{
		runs: map[uuid.UUID][]store.WatcherRun{
			w1: {{Status: "completed", HasAlert: false, Summary: &sum1}},
			w2: {{Status: "completed", HasAlert: false, Summary: &sum2}},
		},
	}
	ce := NewCompositeEvaluator(nil, rs, nil)

	w := makeCompositeWatcher(CompositeConfig{
		Logic: "any_of",
		Conditions: []CompositeCondition{
			{WatcherID: w1.String()},
			{WatcherID: w2.String()},
		},
	})

	result, err := ce.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAlert {
		t.Error("expected no alert when all conditions are false in any_of mode")
	}
}

func TestComposite_NoneOf(t *testing.T) {
	w1, w2 := uuid.New(), uuid.New()
	sum1, sum2 := "ok", "ok"

	rs := &mockRunStoreForComposite{
		runs: map[uuid.UUID][]store.WatcherRun{
			w1: {{Status: "completed", HasAlert: false, Summary: &sum1}},
			w2: {{Status: "completed", HasAlert: false, Summary: &sum2}},
		},
	}
	ce := NewCompositeEvaluator(nil, rs, nil)

	w := makeCompositeWatcher(CompositeConfig{
		Logic: "none_of",
		Conditions: []CompositeCondition{
			{WatcherID: w1.String()},
			{WatcherID: w2.String()},
		},
	})

	result, err := ce.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAlert {
		t.Error("expected no alert when none triggered in none_of mode")
	}
}

func TestComposite_NoneOfOneTriggered(t *testing.T) {
	w1 := uuid.New()
	sum1 := "alert"

	rs := &mockRunStoreForComposite{
		runs: map[uuid.UUID][]store.WatcherRun{
			w1: {{Status: "completed", HasAlert: true, Summary: &sum1}},
		},
	}
	ce := NewCompositeEvaluator(nil, rs, nil)

	w := makeCompositeWatcher(CompositeConfig{
		Logic: "none_of",
		Conditions: []CompositeCondition{
			{WatcherID: w1.String()},
		},
	})

	result, err := ce.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasAlert {
		t.Error("expected alert when condition triggered in none_of mode")
	}
}

func TestComposite_CircularReference(t *testing.T) {
	selfID := uuid.New()

	rs := &mockRunStoreForComposite{runs: map[uuid.UUID][]store.WatcherRun{}}
	ce := NewCompositeEvaluator(nil, rs, nil)

	tc, _ := json.Marshal(CompositeConfig{
		Logic: "all_of",
		Conditions: []CompositeCondition{
			{WatcherID: selfID.String()},
		},
	})

	w := store.Watcher{
		ID:          selfID,
		WatcherType: store.WatcherTypeComposite,
		TypeConfig:  tc,
	}

	_, err := ce.Evaluate(context.Background(), w)
	if err == nil {
		t.Error("expected error for circular reference")
	}
}

func TestComposite_EmptyConditions(t *testing.T) {
	ce := NewCompositeEvaluator(nil, nil, nil)
	w := makeCompositeWatcher(CompositeConfig{
		Logic:      "all_of",
		Conditions: nil,
	})

	_, err := ce.Evaluate(context.Background(), w)
	if err == nil {
		t.Error("expected error for empty conditions")
	}
}
