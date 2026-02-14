package mcp

import (
	"encoding/json"
	"testing"

	"github.com/adham90/opentrace/internal/store"
)

func TestListUsers(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	userStore := store.NewUserStore(db)

	// Create a test user
	userStore.Create(t.Context(), store.CreateUserParams{
		Email:        "test@example.com",
		PasswordHash: "fakehash",
		DisplayName:  "Test User",
		Role:         store.RoleMember,
	})

	handler := listUsersHandler(userStore)
	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)

	var users []map[string]any
	if err := json.Unmarshal([]byte(text), &users); err != nil {
		t.Fatalf("expected JSON array, got: %s", text)
	}
	if len(users) == 0 {
		t.Error("expected at least one user")
	}
	if users[0]["email"] != "test@example.com" {
		t.Errorf("expected test@example.com, got: %v", users[0]["email"])
	}
}

func TestUpdateUserRole(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	userStore := store.NewUserStore(db)
	user, _ := userStore.Create(t.Context(), store.CreateUserParams{
		Email:        "admin@example.com",
		PasswordHash: "fakehash",
		DisplayName:  "Admin",
		Role:         store.RoleMember,
	})

	handler := updateUserRoleHandler(userStore)
	res, err := handler(t.Context(), makeRequest(map[string]any{
		"user_id": user.ID,
		"role":    "admin",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}
	if result["role"] != "admin" {
		t.Errorf("expected admin role, got: %v", result["role"])
	}
}

func TestToggleUserActive(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	userStore := store.NewUserStore(db)
	user, _ := userStore.Create(t.Context(), store.CreateUserParams{
		Email:        "toggle@example.com",
		PasswordHash: "fakehash",
		DisplayName:  "Toggle User",
		Role:         store.RoleMember,
	})

	handler := toggleUserActiveHandler(userStore)
	res, err := handler(t.Context(), makeRequest(map[string]any{
		"user_id":   user.ID,
		"is_active": false,
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}
	if result["status"] != "disabled" {
		t.Errorf("expected disabled, got: %v", result["status"])
	}
}

func TestDeleteUser(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	userStore := store.NewUserStore(db)
	user, _ := userStore.Create(t.Context(), store.CreateUserParams{
		Email:        "delete@example.com",
		PasswordHash: "fakehash",
		DisplayName:  "Delete Me",
		Role:         store.RoleMember,
	})

	handler := deleteUserHandler(userStore)
	res, err := handler(t.Context(), makeRequest(map[string]any{
		"user_id": user.ID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}
	if result["status"] != "deleted" {
		t.Errorf("expected deleted, got: %v", result["status"])
	}

	// Verify user is actually gone
	_, err = userStore.GetByID(t.Context(), user.ID)
	if err == nil {
		t.Error("expected user to be deleted")
	}
}

func TestGetAuditLog(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	auditStore := store.NewAuditStore(db)

	// Create an audit entry
	auditStore.Log(t.Context(), store.LogAuditParams{
		UserID:     "test-user",
		UserEmail:  "test@example.com",
		Action:     "user.create",
		TargetType: "user",
		TargetID:   "new-user",
		Details:    "Created a new user",
	})

	handler := getAuditLogHandler(auditStore)
	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)

	var entries []map[string]any
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatalf("expected JSON array, got: %s", text)
	}
	if len(entries) == 0 {
		t.Error("expected at least one audit entry")
	}
	if entries[0]["action"] != "user.create" {
		t.Errorf("expected user.create action, got: %v", entries[0]["action"])
	}
}

func TestGetAuditLog_Empty(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	auditStore := store.NewAuditStore(db)
	handler := getAuditLogHandler(auditStore)
	res, err := handler(t.Context(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if text != "No audit log entries found." {
		t.Errorf("expected empty message, got: %s", text)
	}
}
