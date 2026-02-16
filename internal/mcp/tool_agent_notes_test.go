package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// mockAgentNoteStore implements store.AgentNoteStore for testing.
type mockAgentNoteStore struct {
	notes map[string]*store.AgentNote // key = "type:id"
	err   error
}

func newMockAgentNoteStore() *mockAgentNoteStore {
	return &mockAgentNoteStore{notes: make(map[string]*store.AgentNote)}
}

func noteKey(entityType, entityID string) string { return entityType + ":" + entityID }

func (m *mockAgentNoteStore) Upsert(_ context.Context, entityType, entityID, note string) (*store.AgentNote, error) {
	if m.err != nil {
		return nil, m.err
	}
	key := noteKey(entityType, entityID)
	now := time.Now()
	if existing, ok := m.notes[key]; ok {
		existing.Note = note
		existing.UpdatedAt = now
		return existing, nil
	}
	n := &store.AgentNote{
		ID:         int64(len(m.notes) + 1),
		EntityType: entityType,
		EntityID:   entityID,
		Note:       note,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	m.notes[key] = n
	return n, nil
}

func (m *mockAgentNoteStore) Get(_ context.Context, entityType, entityID string) (*store.AgentNote, error) {
	if m.err != nil {
		return nil, m.err
	}
	n, ok := m.notes[noteKey(entityType, entityID)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return n, nil
}

func (m *mockAgentNoteStore) List(_ context.Context, entityType string) ([]store.AgentNote, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []store.AgentNote
	for _, n := range m.notes {
		if entityType == "" || n.EntityType == entityType {
			result = append(result, *n)
		}
	}
	return result, nil
}

func (m *mockAgentNoteStore) Delete(_ context.Context, entityType, entityID string) error {
	if m.err != nil {
		return m.err
	}
	key := noteKey(entityType, entityID)
	if _, ok := m.notes[key]; !ok {
		return store.ErrNotFound
	}
	delete(m.notes, key)
	return nil
}

func (m *mockAgentNoteStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

// --- Tests ---

func TestAddNoteHandler_Success(t *testing.T) {
	ns := newMockAgentNoteStore()
	handler := addNoteHandler(ns)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"entity_type": "service",
		"entity_id":   "myapp",
		"note":        "Main web application — handles auth and user management",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["entity_type"] != "service" {
		t.Errorf("entity_type = %v, want service", resp["entity_type"])
	}
}

func TestAddNoteHandler_MissingFields(t *testing.T) {
	ns := newMockAgentNoteStore()
	handler := addNoteHandler(ns)

	// Missing entity_type
	result, _ := handler(context.Background(), makeRequest(map[string]any{
		"entity_id": "x", "note": "y",
	}))
	if !result.IsError {
		t.Error("expected error for missing entity_type")
	}

	// Missing entity_id
	result, _ = handler(context.Background(), makeRequest(map[string]any{
		"entity_type": "service", "note": "y",
	}))
	if !result.IsError {
		t.Error("expected error for missing entity_id")
	}

	// Missing note
	result, _ = handler(context.Background(), makeRequest(map[string]any{
		"entity_type": "service", "entity_id": "x",
	}))
	if !result.IsError {
		t.Error("expected error for missing note")
	}
}

func TestGetNotesHandler_Single(t *testing.T) {
	ns := newMockAgentNoteStore()
	ns.Upsert(context.Background(), "error", "fp1", "Known Redis timeout")

	handler := getNotesHandler(ns)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"entity_type": "error",
		"entity_id":   "fp1",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	var note store.AgentNote
	json.Unmarshal([]byte(text), &note)
	if note.Note != "Known Redis timeout" {
		t.Errorf("note = %q, want 'Known Redis timeout'", note.Note)
	}
}

func TestGetNotesHandler_ListAll(t *testing.T) {
	ns := newMockAgentNoteStore()
	ns.Upsert(context.Background(), "service", "a", "Note A")
	ns.Upsert(context.Background(), "query", "q1", "Note Q")

	handler := getNotesHandler(ns)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["count"].(float64) != 2 {
		t.Errorf("count = %v, want 2", resp["count"])
	}
}

func TestGetNotesHandler_Empty(t *testing.T) {
	ns := newMockAgentNoteStore()
	handler := getNotesHandler(ns)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if text != "No agent notes found. Use add_note to save context for future sessions." {
		t.Errorf("unexpected: %s", text)
	}
}

func TestDeleteNoteHandler_Success(t *testing.T) {
	ns := newMockAgentNoteStore()
	ns.Upsert(context.Background(), "endpoint", "/api/x", "Slow")

	handler := deleteNoteHandler(ns)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"entity_type": "endpoint",
		"entity_id":   "/api/x",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["status"] != "deleted" {
		t.Errorf("status = %v, want deleted", resp["status"])
	}
}

func TestDeleteNoteHandler_NotFound(t *testing.T) {
	ns := newMockAgentNoteStore()
	handler := deleteNoteHandler(ns)
	result, _ := handler(context.Background(), makeRequest(map[string]any{
		"entity_type": "service",
		"entity_id":   "nonexistent",
	}))
	if !result.IsError {
		t.Error("expected error for nonexistent note")
	}
}

func TestAddNoteHandler_NilStore(t *testing.T) {
	handler := addNoteHandler(nil)
	result, _ := handler(context.Background(), makeRequest(map[string]any{
		"entity_type": "service", "entity_id": "x", "note": "y",
	}))
	if !result.IsError {
		t.Error("expected error for nil store")
	}
}
