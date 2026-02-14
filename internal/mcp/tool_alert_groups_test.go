package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// setupAlertGroupTestDB creates an in-memory SQLite DB with all migrations.
func setupAlertGroupTestDB(t *testing.T) (store.AlertGroupStore, store.AlertStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return store.NewAlertGroupStore(db), store.NewAlertStore(db)
}

func TestCreateIncident(t *testing.T) {
	ags, _ := setupAlertGroupTestDB(t)
	handler := createIncidentHandler(ags)

	// Test missing title
	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error for missing title")
	}

	// Test successful creation
	res, err = handler(t.Context(), makeRequest(map[string]any{
		"title":    "DB connection spike",
		"severity": "critical",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "DB connection spike") {
		t.Errorf("expected title in response, got: %s", text)
	}
	if !strings.Contains(text, "critical") {
		t.Errorf("expected severity in response, got: %s", text)
	}
}

func TestListIncidents(t *testing.T) {
	ags, _ := setupAlertGroupTestDB(t)

	// Create two incidents
	ags.Create(t.Context(), store.CreateAlertGroupParams{Title: "Incident 1", Severity: "warning"})
	ags.Create(t.Context(), store.CreateAlertGroupParams{Title: "Incident 2", Severity: "critical"})

	handler := listIncidentsHandler(ags)
	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}

	text := resultText(t, res)
	var groups []store.AlertGroup
	if err := json.Unmarshal([]byte(text), &groups); err != nil {
		t.Fatalf("expected JSON array, got: %s", text)
	}
	if len(groups) != 2 {
		t.Errorf("expected 2 incidents, got %d", len(groups))
	}

	// Test status filter
	res, err = handler(t.Context(), makeRequest(map[string]any{"status": "resolved"}))
	if err != nil {
		t.Fatal(err)
	}
	text = resultText(t, res)
	if !strings.Contains(text, "No incidents") {
		t.Errorf("expected no resolved incidents, got: %s", text)
	}
}

func TestGetIncident(t *testing.T) {
	ags, _ := setupAlertGroupTestDB(t)
	group, _ := ags.Create(t.Context(), store.CreateAlertGroupParams{Title: "Test Incident", Severity: "info"})

	handler := getIncidentHandler(ags)

	// Test missing ID
	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error for missing incident_id")
	}

	// Test valid ID
	res, err = handler(t.Context(), makeRequest(map[string]any{"incident_id": group.ID}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "Test Incident") {
		t.Errorf("expected title in response, got: %s", text)
	}

	// Test not found
	res, err = handler(t.Context(), makeRequest(map[string]any{"incident_id": "nonexistent"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error for nonexistent incident")
	}
}

func TestUpdateIncident(t *testing.T) {
	ags, _ := setupAlertGroupTestDB(t)
	group, _ := ags.Create(t.Context(), store.CreateAlertGroupParams{Title: "Original", Severity: "warning"})

	handler := updateIncidentHandler(ags)

	// Update status to resolved with root cause
	res, err := handler(t.Context(), makeRequest(map[string]any{
		"incident_id": group.ID,
		"status":      "resolved",
		"root_cause":  "Connection pool exhaustion due to leak in checkout service",
		"resolution":  "Deployed connection pool fix v2.1.3",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "resolved") {
		t.Errorf("expected resolved status, got: %s", text)
	}
	if !strings.Contains(text, "Connection pool exhaustion") {
		t.Errorf("expected root cause, got: %s", text)
	}
}

func TestAddAlertsToIncident(t *testing.T) {
	ags, as := setupAlertGroupTestDB(t)
	group, _ := ags.Create(t.Context(), store.CreateAlertGroupParams{Title: "Test Group", Severity: "warning"})

	// Create an alert to add
	alert, err := as.Create(t.Context(), store.CreateAlertParams{
		Title:    "Test Alert",
		Summary:  "Something happened",
		Severity: "warning",
	})
	if err != nil {
		t.Fatalf("creating alert: %v", err)
	}

	handler := addAlertsToIncidentHandler(ags)
	res, err := handler(t.Context(), makeRequest(map[string]any{
		"incident_id": group.ID,
		"alert_ids":   alert.ID.String(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "Added 1 alert(s)") {
		t.Errorf("expected success message, got: %s", text)
	}

	// Verify the alert is in the group
	updated, _ := ags.GetByID(t.Context(), group.ID)
	if updated.AlertCount != 1 {
		t.Errorf("expected 1 alert in group, got %d", updated.AlertCount)
	}
}

func TestAutoCorrelateAlerts(t *testing.T) {
	ags, _ := setupAlertGroupTestDB(t)
	handler := autoCorrelateAlertsHandler(ags)

	// With no alerts, should return "no groups found"
	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "No correlated") {
		t.Errorf("expected no groups message, got: %s", text)
	}
}

func TestDeleteIncident(t *testing.T) {
	ags, _ := setupAlertGroupTestDB(t)
	group, _ := ags.Create(t.Context(), store.CreateAlertGroupParams{Title: "To Delete", Severity: "info"})

	handler := deleteIncidentHandler(ags)
	res, err := handler(t.Context(), makeRequest(map[string]any{"incident_id": group.ID}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "deleted") {
		t.Errorf("expected deleted message, got: %s", text)
	}

	// Verify it's gone
	_, getErr := ags.GetByID(t.Context(), group.ID)
	if getErr == nil {
		t.Error("expected error when getting deleted incident")
	}
}

func TestStopWatcherRun(t *testing.T) {
	hub := watcher.NewEventHub()
	defer hub.Close()

	handler := stopWatcherRunHandler(hub)

	// Test missing run_id
	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error for missing run_id")
	}

	// Test invalid UUID
	res, err = handler(t.Context(), makeRequest(map[string]any{"run_id": "not-a-uuid"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error for invalid UUID")
	}

	// Test unknown run
	res, err = handler(t.Context(), makeRequest(map[string]any{"run_id": "00000000-0000-0000-0000-000000000001"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error for unknown run")
	}
}

func TestDeleteServer(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ss := store.NewServerStore(db)

	// Register a server
	srv, err := ss.Register(t.Context(), store.RegisterServerParams{
		Hostname:  "test-server-01",
		IPAddress: "10.0.0.1",
	})
	if err != nil {
		t.Fatalf("registering server: %v", err)
	}

	handler := deleteServerHandler(ss)

	// Test missing server_id
	res, errr := handler(t.Context(), makeRequest(map[string]any{}))
	if errr != nil {
		t.Fatal(errr)
	}
	if !res.IsError {
		t.Error("expected error for missing server_id")
	}

	// Test delete existing server
	res, errr = handler(t.Context(), makeRequest(map[string]any{"server_id": srv.ID.String()}))
	if errr != nil {
		t.Fatal(errr)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "deleted successfully") {
		t.Errorf("expected success message, got: %s", text)
	}

	// Verify it's gone
	_, getErr := ss.GetByID(t.Context(), srv.ID)
	if getErr == nil {
		t.Error("expected error when getting deleted server")
	}
}
