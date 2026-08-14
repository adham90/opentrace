package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

func TestHandleUpdateRole_MissingUserID(t *testing.T) {
	userStore := mocks.NewUserStore()
	d := AdminDeps{UserStore: userStore}

	args := map[string]any{"role": "admin"}
	result, err := HandleUpdateRole(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when user_id is missing")
	}
}

func TestHandleToggleActive_MissingUserID(t *testing.T) {
	userStore := mocks.NewUserStore()
	d := AdminDeps{UserStore: userStore}

	args := map[string]any{"is_active": true}
	result, err := HandleToggleActive(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when user_id is missing")
	}
}

func TestHandleDeleteUser_MissingUserID(t *testing.T) {
	userStore := mocks.NewUserStore()
	d := AdminDeps{UserStore: userStore}

	args := map[string]any{}
	result, err := HandleDeleteUser(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when user_id is missing")
	}
}

func TestHandleUpdateRole_ValidParams(t *testing.T) {
	userStore := mocks.NewUserStore()

	// Seed a user.
	user, err := userStore.Create(context.Background(), store.CreateUserParams{
		Email:       "role-test@example.com",
		DisplayName: "Role Test",
		Role:        store.RoleMember,
	})
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	d := AdminDeps{UserStore: userStore}
	args := map[string]any{
		"user_id": user.ID,
		"role":    "admin",
	}

	result, err := HandleUpdateRole(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		text := extractText(t, result)
		t.Errorf("expected IsError to be false, got error: %s", text)
	}

	text := extractText(t, result)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if parsed["status"] != "updated" {
		t.Errorf("status = %v, want %q", parsed["status"], "updated")
	}
	if parsed["role"] != "admin" {
		t.Errorf("role = %v, want %q", parsed["role"], "admin")
	}
	if parsed["user_id"] != user.ID {
		t.Errorf("user_id = %v, want %q", parsed["user_id"], user.ID)
	}
}

func TestHandleToggleActive_ValidParams(t *testing.T) {
	userStore := mocks.NewUserStore()

	// Seed a user (defaults to active=true).
	user, err := userStore.Create(context.Background(), store.CreateUserParams{
		Email:       "toggle-test@example.com",
		DisplayName: "Toggle Test",
		Role:        store.RoleMember,
	})
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	d := AdminDeps{UserStore: userStore}

	// Disable the user.
	args := map[string]any{
		"user_id":   user.ID,
		"is_active": false,
	}

	result, err := HandleToggleActive(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		text := extractText(t, result)
		t.Errorf("expected IsError to be false, got error: %s", text)
	}

	text := extractText(t, result)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if parsed["status"] != "disabled" {
		t.Errorf("status = %v, want %q", parsed["status"], "disabled")
	}
	if parsed["is_active"] != false {
		t.Errorf("is_active = %v, want false", parsed["is_active"])
	}
}

func TestHandleDeleteUser_ValidParams(t *testing.T) {
	userStore := mocks.NewUserStore()

	// Seed a user.
	user, err := userStore.Create(context.Background(), store.CreateUserParams{
		Email:       "delete-test@example.com",
		DisplayName: "Delete Test",
		Role:        store.RoleMember,
	})
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	d := AdminDeps{UserStore: userStore}
	args := map[string]any{
		"user_id": user.ID,
	}

	result, err := HandleDeleteUser(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		text := extractText(t, result)
		t.Errorf("expected IsError to be false, got error: %s", text)
	}

	text := extractText(t, result)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if parsed["status"] != "deleted" {
		t.Errorf("status = %v, want %q", parsed["status"], "deleted")
	}
	if parsed["email"] != "delete-test@example.com" {
		t.Errorf("email = %v, want %q", parsed["email"], "delete-test@example.com")
	}

	// Verify user was actually removed from the store.
	_, getErr := userStore.GetByID(context.Background(), user.ID)
	if getErr != store.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", getErr)
	}
}

// TestHandleUpdateRetention_PreservesMetricRetention covers the lost update:
// SetRetention persists the whole struct, so building a fresh one wiped a
// separately configured metric_retention_days back to 0 ("follow the global
// window").
func TestHandleUpdateRetention_PreservesMetricRetention(t *testing.T) {
	settings := mocks.NewSettingsStore()
	settings.Retention = &store.RetentionSettings{RetentionDays: 30, MetricRetentionDays: 90}
	d := AdminDeps{SettingsStore: settings}

	result, err := HandleUpdateRetention(context.Background(), d, map[string]any{"retention_days": float64(14)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	got, err := settings.GetRetention(context.Background())
	if err != nil {
		t.Fatalf("reading back settings: %v", err)
	}
	if got.RetentionDays != 14 {
		t.Errorf("retention_days = %d, want 14", got.RetentionDays)
	}
	if got.MetricRetentionDays != 90 {
		t.Errorf("metric_retention_days = %d, want 90 (it must not be clobbered)", got.MetricRetentionDays)
	}
}

// The confirmation text must describe what the setting actually prunes. Watch
// runs and watch alerts are governed by the separate retention_policy blob.
func TestHandleUpdateRetention_MessageDoesNotClaimWatchPruning(t *testing.T) {
	settings := mocks.NewSettingsStore()
	d := AdminDeps{SettingsStore: settings}

	result, err := HandleUpdateRetention(context.Background(), d, map[string]any{"retention_days": float64(7)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractText(t, result)
	if !strings.Contains(text, "retention_policy") {
		t.Errorf("expected the message to point at retention_policy for watch data:\n%s", text)
	}
	for _, want := range []string{"Logs", "audit", "error groups"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in the confirmation message:\n%s", want, text)
		}
	}
}

func TestHandleUpdateRetention_RejectsOutOfRange(t *testing.T) {
	d := AdminDeps{SettingsStore: mocks.NewSettingsStore()}
	for _, days := range []float64{0, 366} {
		result, err := HandleUpdateRetention(context.Background(), d, map[string]any{"retention_days": days})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Errorf("retention_days=%v should have been rejected", days)
		}
	}
}
