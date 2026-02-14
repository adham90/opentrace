package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/store"
)

// mockWatcherStoreWithUpdate extends mockWatcherStore with actual Update support.
type mockWatcherStoreWithUpdate struct {
	mockWatcherStore
	updated *store.Watcher
}

func (m *mockWatcherStoreWithUpdate) Update(_ context.Context, id uuid.UUID, params store.UpdateWatcherParams) (*store.Watcher, error) {
	for _, w := range m.watchers {
		if w.ID == id {
			if params.Title != nil {
				w.Title = *params.Title
			}
			if params.Description != nil {
				w.Description = *params.Description
			}
			if params.Severity != nil {
				w.Severity = *params.Severity
			}
			m.updated = &w
			return &w, nil
		}
	}
	return nil, store.ErrNotFound
}

func TestUpdateWatcherHandler_Success(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStoreWithUpdate{
		mockWatcherStore: mockWatcherStore{
			watchers: []store.Watcher{
				{ID: watcherID, Title: "Old Title", Status: store.WatcherActive, WatcherType: store.WatcherTypeAI, CreatedAt: time.Now()},
			},
		},
	}
	handler := updateWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
		"title":      "New Title",
		"severity":   "critical",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "updated successfully") {
		t.Errorf("expected 'updated successfully', got: %s", text)
	}
}

func TestUpdateWatcherHandler_NoFields(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStoreWithUpdate{
		mockWatcherStore: mockWatcherStore{
			watchers: []store.Watcher{
				{ID: watcherID, Title: "Watcher", Status: store.WatcherActive},
			},
		},
	}
	handler := updateWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for no fields to update")
	}
}

func TestUpdateWatcherHandler_NotFound(t *testing.T) {
	ws := &mockWatcherStoreWithUpdate{
		mockWatcherStore: mockWatcherStore{},
	}
	handler := updateWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": uuid.New().String(),
		"title":      "New",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for watcher not found")
	}
}

func TestUpdateWatcherHandler_RuleConfigOnAI(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStoreWithUpdate{
		mockWatcherStore: mockWatcherStore{
			watchers: []store.Watcher{
				{ID: watcherID, Title: "AI Watcher", WatcherType: store.WatcherTypeAI, Status: store.WatcherActive},
			},
		},
	}
	handler := updateWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id":  watcherID.String(),
		"rule_config": `{"source":"query","query":"SELECT 1","metric":"value","operator":"gt","threshold":100}`,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when setting rule_config on AI watcher")
	}

	text := resultText(t, result)
	if !contains(text, "rule_config can only be set on rule watchers") {
		t.Errorf("expected rule_config error message, got: %s", text)
	}
}

func TestDeleteWatcherHandler_Success(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: watcherID, Title: "Delete Me", Status: store.WatcherActive},
		},
	}
	handler := deleteWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "deleted successfully") {
		t.Errorf("expected 'deleted successfully', got: %s", text)
	}
	if !contains(text, "Delete Me") {
		t.Errorf("expected watcher title in response, got: %s", text)
	}
}

func TestDeleteWatcherHandler_NotFound(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := deleteWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": uuid.New().String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for watcher not found")
	}
}

func TestPauseWatcherHandler_Success(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: watcherID, Title: "Active Watcher", Status: store.WatcherActive},
		},
	}
	handler := pauseWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "paused") {
		t.Errorf("expected 'paused', got: %s", text)
	}
}

func TestPauseWatcherHandler_AlreadyPaused(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: watcherID, Title: "Paused Watcher", Status: store.WatcherPaused},
		},
	}
	handler := pauseWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "already paused") {
		t.Errorf("expected 'already paused', got: %s", text)
	}
}

func TestResumeWatcherHandler_Success(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: watcherID, Title: "Paused Watcher", Status: store.WatcherPaused},
		},
	}
	handler := resumeWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "resumed") {
		t.Errorf("expected 'resumed', got: %s", text)
	}
}

func TestResumeWatcherHandler_AlreadyActive(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: watcherID, Title: "Active Watcher", Status: store.WatcherActive},
		},
	}
	handler := resumeWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "already active") {
		t.Errorf("expected 'already active', got: %s", text)
	}
}

func TestUpdateWatcherHandler_ExpiresAt(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStoreWithUpdate{
		mockWatcherStore: mockWatcherStore{
			watchers: []store.Watcher{
				{ID: watcherID, Title: "Watcher", Status: store.WatcherActive, WatcherType: store.WatcherTypeAI, CreatedAt: time.Now()},
			},
		},
	}
	handler := updateWatcherHandler(ws)

	// Set expires_at
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
		"expires_at": "2099-03-01T00:00:00Z",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}
	text := resultText(t, result)
	if !contains(text, "updated successfully") {
		t.Errorf("expected 'updated successfully', got: %s", text)
	}
}

func TestUpdateWatcherHandler_ExpiresAtNever(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStoreWithUpdate{
		mockWatcherStore: mockWatcherStore{
			watchers: []store.Watcher{
				{ID: watcherID, Title: "Watcher", Status: store.WatcherActive, WatcherType: store.WatcherTypeAI, CreatedAt: time.Now()},
			},
		},
	}
	handler := updateWatcherHandler(ws)

	// Clear expires_at with "never"
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
		"expires_at": "never",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}
}

func TestUpdateWatcherHandler_ExpiresAtInvalid(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStoreWithUpdate{
		mockWatcherStore: mockWatcherStore{
			watchers: []store.Watcher{
				{ID: watcherID, Title: "Watcher", Status: store.WatcherActive, WatcherType: store.WatcherTypeAI, CreatedAt: time.Now()},
			},
		},
	}
	handler := updateWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
		"expires_at": "not-a-date",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid expires_at format")
	}
	text := resultText(t, result)
	if !contains(text, "invalid expires_at") {
		t.Errorf("expected 'invalid expires_at' error, got: %s", text)
	}
}

func TestResumeWatcherHandler_Expired(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: watcherID, Title: "Expired Watcher", Status: store.WatcherExpired},
		},
	}
	handler := resumeWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}
	text := resultText(t, result)
	if !contains(text, "reactivated") {
		t.Errorf("expected 'reactivated', got: %s", text)
	}
}

func TestCreateWatcherHandler_WithExpiresAt(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := createWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"title":       "Deploy monitor",
		"description": "Watch the deploy",
		"expires_at":  "2099-06-01T12:00:00Z",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}
	text := resultText(t, result)
	if !contains(text, "created successfully") {
		t.Errorf("expected 'created successfully', got: %s", text)
	}
}

func TestCreateWatcherHandler_InvalidExpiresAt(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := createWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"title":       "Deploy monitor",
		"description": "Watch the deploy",
		"expires_at":  "tomorrow",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid expires_at")
	}
	text := resultText(t, result)
	if !contains(text, "invalid expires_at") {
		t.Errorf("expected 'invalid expires_at' error, got: %s", text)
	}
}

// Verify JSON response structure for update.
func TestUpdateWatcherHandler_ResponseFormat(t *testing.T) {
	watcherID := uuid.New()
	ws := &mockWatcherStoreWithUpdate{
		mockWatcherStore: mockWatcherStore{
			watchers: []store.Watcher{
				{ID: watcherID, Title: "Old Title", Status: store.WatcherActive, WatcherType: store.WatcherTypeAI, CreatedAt: time.Now()},
			},
		},
	}
	handler := updateWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": watcherID.String(),
		"title":      "Updated Title",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	// The response should contain valid JSON after the success message.
	if !contains(text, "Updated Title") {
		t.Errorf("expected 'Updated Title' in response, got: %s", text)
	}

	// Extract JSON portion after the success message line.
	jsonStart := 0
	for i, c := range text {
		if c == '{' {
			jsonStart = i
			break
		}
	}
	if jsonStart > 0 {
		var w map[string]any
		if err := json.Unmarshal([]byte(text[jsonStart:]), &w); err != nil {
			t.Fatalf("response contains invalid JSON: %v", err)
		}
	}
}
