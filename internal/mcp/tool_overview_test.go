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

	serverStore := store.NewServerStore(db)
	logStore := store.NewLogStore(db)
	errorGroupStore := store.NewErrorGroupStore(db)
	watchStore := store.NewWatchStore(db)
	healthCheckStore := store.NewHealthCheckStore(db)

	handler := systemOverviewHandler(overviewDeps{
		logStore:         logStore,
		serverStore:      serverStore,
		errorGroupStore:  errorGroupStore,
		watchStore:       watchStore,
		healthCheckStore: healthCheckStore,
	})

	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}

	text := resultText(t, res)
	var overview map[string]any
	if err := json.Unmarshal([]byte(text), &overview); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}

	// Should have servers section
	if _, ok := overview["servers"]; !ok {
		t.Error("expected servers section in overview")
	}
}

func TestTriageAlerts_Empty(t *testing.T) {
	handler := triageAlertsHandler(overviewDeps{})
	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "healthy") {
		t.Errorf("expected healthy message, got: %s", text)
	}
}
