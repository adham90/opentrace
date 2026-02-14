package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/store"
)

// setupTestWatcherStore creates a real SQLite-backed WatcherStore for testing.
func setupTestWatcherStore(t *testing.T) store.WatcherStore {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.RunSQLiteMigrations(db); err != nil {
		t.Fatalf("running migrations: %v", err)
	}
	return store.NewWatcherStore(db)
}

// createTestWatcher is a helper that creates a watcher via the real store.
func createTestWatcher(t *testing.T, ws store.WatcherStore, params store.CreateWatcherParams) *store.Watcher {
	t.Helper()
	if params.Title == "" {
		params.Title = "Test Watcher"
	}
	if params.Description == "" {
		params.Description = "test"
	}
	w, err := ws.Create(context.Background(), params)
	if err != nil {
		t.Fatalf("creating test watcher: %v", err)
	}
	return w
}

func TestUpdateWatcherHandler_Success(t *testing.T) {
	ws := setupTestWatcherStore(t)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{Title: "Old Title"})
	handler := updateWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": w.ID.String(),
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

	// Verify via real store
	got, _ := ws.GetByID(context.Background(), w.ID)
	if got.Title != "New Title" {
		t.Errorf("store title = %q, want %q", got.Title, "New Title")
	}
	if got.Severity != store.SeverityCritical {
		t.Errorf("store severity = %q, want %q", got.Severity, store.SeverityCritical)
	}
}

func TestUpdateWatcherHandler_NoFields(t *testing.T) {
	ws := setupTestWatcherStore(t)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{Title: "Watcher"})
	handler := updateWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": w.ID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for no fields to update")
	}
}

func TestUpdateWatcherHandler_NotFound(t *testing.T) {
	ws := setupTestWatcherStore(t)
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
	ws := setupTestWatcherStore(t)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{
		Title:       "AI Watcher",
		WatcherType: store.WatcherTypeAI,
	})
	handler := updateWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id":  w.ID.String(),
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
	ws := setupTestWatcherStore(t)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{Title: "Delete Me"})
	handler := deleteWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": w.ID.String(),
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

	// Verify actually deleted
	_, err = ws.GetByID(context.Background(), w.ID)
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestDeleteWatcherHandler_NotFound(t *testing.T) {
	ws := setupTestWatcherStore(t)
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
	ws := setupTestWatcherStore(t)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{Title: "Active Watcher"})
	handler := pauseWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": w.ID.String(),
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

	// Verify status in store
	got, _ := ws.GetByID(context.Background(), w.ID)
	if got.Status != store.WatcherPaused {
		t.Errorf("store status = %q, want %q", got.Status, store.WatcherPaused)
	}
}

func TestPauseWatcherHandler_AlreadyPaused(t *testing.T) {
	ws := setupTestWatcherStore(t)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{Title: "Paused Watcher"})
	// Pause it first
	ws.UpdateStatus(context.Background(), w.ID, store.WatcherPaused)

	handler := pauseWatcherHandler(ws)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": w.ID.String(),
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
	ws := setupTestWatcherStore(t)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{Title: "Paused Watcher"})
	// Pause it first, then resume
	ws.UpdateStatus(context.Background(), w.ID, store.WatcherPaused)

	handler := resumeWatcherHandler(ws)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": w.ID.String(),
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

	// Verify status in store
	got, _ := ws.GetByID(context.Background(), w.ID)
	if got.Status != store.WatcherActive {
		t.Errorf("store status = %q, want %q", got.Status, store.WatcherActive)
	}
}

func TestResumeWatcherHandler_AlreadyActive(t *testing.T) {
	ws := setupTestWatcherStore(t)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{Title: "Active Watcher"})

	handler := resumeWatcherHandler(ws)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": w.ID.String(),
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
	ws := setupTestWatcherStore(t)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{Title: "Watcher"})
	handler := updateWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": w.ID.String(),
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

	// Verify expires_at persisted
	got, _ := ws.GetByID(context.Background(), w.ID)
	if got.ExpiresAt == nil {
		t.Fatal("expected expires_at to be set in store")
	}
	if got.ExpiresAt.Year() != 2099 {
		t.Errorf("expires_at year = %d, want 2099", got.ExpiresAt.Year())
	}
}

func TestUpdateWatcherHandler_ExpiresAtNever(t *testing.T) {
	ws := setupTestWatcherStore(t)
	ea := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{
		Title:     "Watcher",
		ExpiresAt: &ea,
	})
	handler := updateWatcherHandler(ws)

	// Clear expires_at with "never"
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": w.ID.String(),
		"expires_at": "never",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	// Verify expires_at cleared
	got, _ := ws.GetByID(context.Background(), w.ID)
	if got.ExpiresAt != nil {
		t.Errorf("expected expires_at to be nil after 'never', got: %v", got.ExpiresAt)
	}
}

func TestUpdateWatcherHandler_ExpiresAtInvalid(t *testing.T) {
	ws := setupTestWatcherStore(t)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{Title: "Watcher"})
	handler := updateWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": w.ID.String(),
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
	ws := setupTestWatcherStore(t)
	// Create watcher with expires_at in the past
	past := time.Now().Add(-1 * time.Hour)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{
		Title:     "Expired Watcher",
		ExpiresAt: &past,
	})

	// Expire it via the real store method
	n, err := ws.ExpireWatchers(context.Background())
	if err != nil {
		t.Fatalf("ExpireWatchers: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 expired, got %d", n)
	}

	// Verify it's expired
	got, _ := ws.GetByID(context.Background(), w.ID)
	if got.Status != store.WatcherExpired {
		t.Fatalf("expected expired status, got %q", got.Status)
	}

	// Resume via MCP handler
	handler := resumeWatcherHandler(ws)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": w.ID.String(),
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

	// Verify reactivated in store
	got, _ = ws.GetByID(context.Background(), w.ID)
	if got.Status != store.WatcherActive {
		t.Errorf("expected active after resume, got %q", got.Status)
	}
}

func TestCreateWatcherHandler_WithExpiresAt(t *testing.T) {
	ws := setupTestWatcherStore(t)
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

	// Verify in store
	watchers, _ := ws.List(context.Background(), store.ListWatcherParams{})
	if len(watchers) != 1 {
		t.Fatalf("expected 1 watcher, got %d", len(watchers))
	}
	if watchers[0].ExpiresAt == nil {
		t.Fatal("expected expires_at to be set in store")
	}
}

func TestCreateWatcherHandler_InvalidExpiresAt(t *testing.T) {
	ws := setupTestWatcherStore(t)
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

	// Verify nothing was created
	watchers, _ := ws.List(context.Background(), store.ListWatcherParams{})
	if len(watchers) != 0 {
		t.Errorf("expected 0 watchers after invalid create, got %d", len(watchers))
	}
}

// Verify JSON response structure for update.
func TestUpdateWatcherHandler_ResponseFormat(t *testing.T) {
	ws := setupTestWatcherStore(t)
	w := createTestWatcher(t, ws, store.CreateWatcherParams{Title: "Old Title"})
	handler := updateWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"watcher_id": w.ID.String(),
		"title":      "Updated Title",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
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
