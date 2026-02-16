package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// mockErrorGroupStore implements store.ErrorGroupStore for testing.
type mockErrorGroupStore struct {
	groups map[string]*store.ErrorGroup
	events map[string][]store.ErrorGroupEvent
	err    error
}

func newMockErrorGroupStore() *mockErrorGroupStore {
	return &mockErrorGroupStore{
		groups: make(map[string]*store.ErrorGroup),
		events: make(map[string][]store.ErrorGroupEvent),
	}
}

func (m *mockErrorGroupStore) Upsert(_ context.Context, entry store.LogEntry) error {
	if m.err != nil {
		return m.err
	}
	if g, ok := m.groups[entry.ErrorFingerprint]; ok {
		g.OccurrenceCount++
		g.LastSeenAt = entry.Timestamp
		return nil
	}
	m.groups[entry.ErrorFingerprint] = &store.ErrorGroup{
		Fingerprint:     entry.ErrorFingerprint,
		Service:         entry.Service,
		ExceptionClass:  entry.ExceptionClass,
		Message:         entry.Message,
		Status:          store.ErrorGroupUnresolved,
		OccurrenceCount: 1,
		FirstSeenAt:     entry.Timestamp,
		LastSeenAt:      entry.Timestamp,
	}
	return nil
}

func (m *mockErrorGroupStore) Get(_ context.Context, fingerprint string) (*store.ErrorGroup, error) {
	if m.err != nil {
		return nil, m.err
	}
	g, ok := m.groups[fingerprint]
	if !ok {
		return nil, store.ErrNotFound
	}
	return g, nil
}

func (m *mockErrorGroupStore) List(_ context.Context, params store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []store.ErrorGroup
	for _, g := range m.groups {
		if params.Status != "" && g.Status != params.Status {
			continue
		}
		if params.Service != "" && g.Service != params.Service {
			continue
		}
		result = append(result, *g)
	}
	return result, nil
}

func (m *mockErrorGroupStore) Count(_ context.Context, status store.ErrorGroupStatus) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	count := 0
	for _, g := range m.groups {
		if status == "" || g.Status == status {
			count++
		}
	}
	return count, nil
}

func (m *mockErrorGroupStore) Resolve(_ context.Context, fingerprint string, reason string) error {
	if m.err != nil {
		return m.err
	}
	g, ok := m.groups[fingerprint]
	if !ok {
		return store.ErrNotFound
	}
	g.Status = store.ErrorGroupResolved
	m.events[fingerprint] = append(m.events[fingerprint], store.ErrorGroupEvent{
		Fingerprint: fingerprint, Action: "resolved", Reason: reason, CreatedAt: time.Now(),
	})
	return nil
}

func (m *mockErrorGroupStore) Ignore(_ context.Context, fingerprint string, reason string) error {
	if m.err != nil {
		return m.err
	}
	g, ok := m.groups[fingerprint]
	if !ok {
		return store.ErrNotFound
	}
	g.Status = store.ErrorGroupIgnored
	m.events[fingerprint] = append(m.events[fingerprint], store.ErrorGroupEvent{
		Fingerprint: fingerprint, Action: "ignored", Reason: reason, CreatedAt: time.Now(),
	})
	return nil
}

func (m *mockErrorGroupStore) ListEvents(_ context.Context, fingerprint string, limit int) ([]store.ErrorGroupEvent, error) {
	evts := m.events[fingerprint]
	if len(evts) > limit {
		evts = evts[:limit]
	}
	return evts, nil
}

func (m *mockErrorGroupStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

// --- Tests ---

func TestErrorGroupsHandler_Success(t *testing.T) {
	egs := newMockErrorGroupStore()
	egs.groups["fp1"] = &store.ErrorGroup{
		Fingerprint:     "fp1",
		Service:         "myapp",
		ExceptionClass:  "Redis::TimeoutError",
		Message:         "Connection timed out",
		Status:          store.ErrorGroupUnresolved,
		OccurrenceCount: 42,
		FirstSeenAt:     time.Now().Add(-24 * time.Hour),
		LastSeenAt:      time.Now(),
	}
	egs.groups["fp2"] = &store.ErrorGroup{
		Fingerprint:     "fp2",
		Service:         "myapp",
		ExceptionClass:  "ActiveRecord::StatementInvalid",
		Message:         "PG::ConnectionBad",
		Status:          store.ErrorGroupUnresolved,
		OccurrenceCount: 5,
		FirstSeenAt:     time.Now().Add(-1 * time.Hour),
		LastSeenAt:      time.Now(),
	}

	handler := errorGroupsHandler(egs)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["total_unresolved"] != float64(2) {
		t.Errorf("total_unresolved = %v, want 2", resp["total_unresolved"])
	}
	groups, ok := resp["error_groups"].([]any)
	if !ok {
		t.Fatal("expected error_groups array")
	}
	if len(groups) != 2 {
		t.Errorf("len(error_groups) = %d, want 2", len(groups))
	}
}

func TestErrorGroupsHandler_Empty(t *testing.T) {
	egs := newMockErrorGroupStore()

	handler := errorGroupsHandler(egs)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "No error groups found") {
		t.Errorf("expected empty message, got: %s", text)
	}
}

func TestErrorGroupsHandler_NilStore(t *testing.T) {
	handler := errorGroupsHandler(nil)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when store is nil")
	}
}

func TestErrorDetailHandler_Success(t *testing.T) {
	egs := newMockErrorGroupStore()
	egs.groups["fp1"] = &store.ErrorGroup{
		Fingerprint:     "fp1",
		Service:         "myapp",
		ExceptionClass:  "Redis::TimeoutError",
		Message:         "Connection timed out",
		Status:          store.ErrorGroupUnresolved,
		OccurrenceCount: 10,
		FirstSeenAt:     time.Now().Add(-24 * time.Hour),
		LastSeenAt:      time.Now(),
	}
	egs.events["fp1"] = []store.ErrorGroupEvent{
		{ID: 1, Fingerprint: "fp1", Action: "reopened", Reason: "recurred", CreatedAt: time.Now()},
	}

	handler := errorDetailHandler(egs, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{"fingerprint": "fp1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["fingerprint"] != "fp1" {
		t.Errorf("fingerprint = %v, want fp1", resp["fingerprint"])
	}
	if resp["occurrence_count"] != float64(10) {
		t.Errorf("occurrence_count = %v, want 10", resp["occurrence_count"])
	}
	events, ok := resp["events"].([]any)
	if !ok {
		t.Fatal("expected events array")
	}
	if len(events) != 1 {
		t.Errorf("len(events) = %d, want 1", len(events))
	}
}

func TestErrorDetailHandler_NotFound(t *testing.T) {
	egs := newMockErrorGroupStore()
	handler := errorDetailHandler(egs, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{"fingerprint": "nonexistent"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for nonexistent fingerprint")
	}
}

func TestErrorDetailHandler_MissingFingerprint(t *testing.T) {
	egs := newMockErrorGroupStore()
	handler := errorDetailHandler(egs, nil)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when fingerprint missing")
	}
}

func TestResolveErrorHandler_Success(t *testing.T) {
	egs := newMockErrorGroupStore()
	egs.groups["fp1"] = &store.ErrorGroup{
		Fingerprint: "fp1", Status: store.ErrorGroupUnresolved,
	}

	handler := resolveErrorHandler(egs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"fingerprint": "fp1",
		"reason":      "Fixed in PR #42",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "resolved") {
		t.Errorf("expected 'resolved' in response, got: %s", text)
	}

	if egs.groups["fp1"].Status != store.ErrorGroupResolved {
		t.Errorf("status = %q, want resolved", egs.groups["fp1"].Status)
	}
}

func TestResolveErrorHandler_MissingArgs(t *testing.T) {
	egs := newMockErrorGroupStore()
	handler := resolveErrorHandler(egs)

	// Missing fingerprint.
	result, _ := handler(context.Background(), makeRequest(map[string]any{"reason": "test"}))
	if !result.IsError {
		t.Fatal("expected error when fingerprint missing")
	}

	// Missing reason.
	result, _ = handler(context.Background(), makeRequest(map[string]any{"fingerprint": "fp1"}))
	if !result.IsError {
		t.Fatal("expected error when reason missing")
	}
}

func TestIgnoreErrorHandler_Success(t *testing.T) {
	egs := newMockErrorGroupStore()
	egs.groups["fp1"] = &store.ErrorGroup{
		Fingerprint: "fp1", Status: store.ErrorGroupUnresolved,
	}

	handler := ignoreErrorHandler(egs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"fingerprint": "fp1",
		"reason":      "Known noise",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	if egs.groups["fp1"].Status != store.ErrorGroupIgnored {
		t.Errorf("status = %q, want ignored", egs.groups["fp1"].Status)
	}
}
