package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/store"
)

func TestSystemOverview(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	alertStore := store.NewAlertStore(db)
	watcherStore := store.NewWatcherStore(db)
	serverStore := store.NewServerStore(db)

	handler := systemOverviewHandler(alertStore, watcherStore, nil, nil, serverStore)

	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}

	text := resultText(t, res)
	var overview map[string]any
	if err := json.Unmarshal([]byte(text), &overview); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}

	// Should have alerts, watchers, and servers sections
	if _, ok := overview["alerts"]; !ok {
		t.Error("expected alerts section in overview")
	}
	if _, ok := overview["watchers"]; !ok {
		t.Error("expected watchers section in overview")
	}
	if _, ok := overview["servers"]; !ok {
		t.Error("expected servers section in overview")
	}
}

func TestTriageAlerts_Empty(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	alertStore := store.NewAlertStore(db)
	runStore := store.NewWatcherRunStore(db)

	handler := triageAlertsHandler(alertStore, runStore, nil, nil)
	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "healthy") {
		t.Errorf("expected healthy message, got: %s", text)
	}
}

func TestTriageAlerts_WithAlerts(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	alertStore := store.NewAlertStore(db)
	runStore := store.NewWatcherRunStore(db)

	// Create an alert
	alertStore.Create(t.Context(), store.CreateAlertParams{
		Title:    "High CPU usage",
		Summary:  "CPU at 95%",
		Severity: "critical",
	})

	handler := triageAlertsHandler(alertStore, runStore, nil, nil)
	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)

	var items []triageEntry
	if err := json.Unmarshal([]byte(text), &items); err != nil {
		t.Fatalf("expected JSON array, got: %s", text)
	}
	if len(items) == 0 {
		t.Error("expected at least one triage item")
	}
	if items[0].Title != "High CPU usage" {
		t.Errorf("expected 'High CPU usage', got: %s", items[0].Title)
	}
}

