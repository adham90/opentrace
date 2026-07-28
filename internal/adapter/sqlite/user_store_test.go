package sqlite

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/pkg/store"
)

func TestUserStore_CreateAndGetByID(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	user, err := us.Create(ctx, store.CreateUserParams{
		Email:        "alice@example.com",
		PasswordHash: "hash123",
		DisplayName:  "Alice",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if user.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if user.Email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", user.Email, "alice@example.com")
	}
	if user.PasswordHash != "hash123" {
		t.Errorf("PasswordHash = %q, want %q", user.PasswordHash, "hash123")
	}
	if user.DisplayName != "Alice" {
		t.Errorf("DisplayName = %q, want %q", user.DisplayName, "Alice")
	}
	if user.Role != store.RoleAdmin {
		t.Errorf("Role = %q, want %q", user.Role, store.RoleAdmin)
	}
	if user.MCPEnabled {
		t.Error("expected MCPEnabled = false")
	}
	if !user.IsActive {
		t.Error("expected IsActive = true")
	}
	if user.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if user.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}

	// GetByID should return the same user.
	got, err := us.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("GetByID ID = %q, want %q", got.ID, user.ID)
	}
	if got.Email != user.Email {
		t.Errorf("GetByID Email = %q, want %q", got.Email, user.Email)
	}
}

func TestUserStore_GetByEmail(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	_, err := us.Create(ctx, store.CreateUserParams{
		Email:        "bob@example.com",
		PasswordHash: "hash456",
		DisplayName:  "Bob",
		Role:         store.RoleMember,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := us.GetByEmail(ctx, "bob@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.Email != "bob@example.com" {
		t.Errorf("Email = %q, want %q", got.Email, "bob@example.com")
	}
	if got.DisplayName != "Bob" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Bob")
	}

	// Non-existent email should return ErrNotFound.
	_, err = us.GetByEmail(ctx, "nobody@example.com")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestUserStore_GetByMCPToken(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	token := "mcp-secret-token-123"

	// Create user, then set MCP token and enable MCP.
	user, err := us.Create(ctx, store.CreateUserParams{
		Email:        "carol@example.com",
		PasswordHash: "hash789",
		DisplayName:  "Carol",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := us.UpdateMCPToken(ctx, user.ID, token); err != nil {
		t.Fatalf("UpdateMCPToken: %v", err)
	}

	trueVal := true
	_, err = us.Update(ctx, user.ID, store.UpdateUserParams{MCPEnabled: &trueVal})
	if err != nil {
		t.Fatalf("Update MCPEnabled: %v", err)
	}

	// Should find the user by token now.
	got, err := us.GetByMCPToken(ctx, token)
	if err != nil {
		t.Fatalf("GetByMCPToken: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("ID = %q, want %q", got.ID, user.ID)
	}

	// Disable MCP -- GetByMCPToken should return ErrNotFound.
	falseVal := false
	_, err = us.Update(ctx, user.ID, store.UpdateUserParams{MCPEnabled: &falseVal})
	if err != nil {
		t.Fatalf("Update MCPEnabled=false: %v", err)
	}
	_, err = us.GetByMCPToken(ctx, token)
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound for disabled MCP, got: %v", err)
	}

	// Re-enable MCP, but deactivate user -- should still return ErrNotFound.
	_, err = us.Update(ctx, user.ID, store.UpdateUserParams{MCPEnabled: &trueVal})
	if err != nil {
		t.Fatalf("re-enable MCP: %v", err)
	}

	// Need a second admin so we can deactivate this one.
	_, err = us.Create(ctx, store.CreateUserParams{
		Email:        "admin2@example.com",
		PasswordHash: "hash",
		DisplayName:  "Admin2",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create second admin: %v", err)
	}

	_, err = us.Update(ctx, user.ID, store.UpdateUserParams{IsActive: &falseVal})
	if err != nil {
		t.Fatalf("Update IsActive=false: %v", err)
	}
	_, err = us.GetByMCPToken(ctx, token)
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound for inactive user, got: %v", err)
	}
}

// A token handed out by Create must authenticate immediately. This is the /connect
// first-admin path: it passes MCPToken to Create and returns that token to the caller,
// so if Create leaves mcp_enabled at its schema default of 0 the token 401s as
// "invalid or disabled" forever — and only a second connect (which flips the flag)
// recovers it.
func TestUserStore_CreateWithMCPToken_IsUsable(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	token := "mcp_created_with_token"
	user, err := us.Create(ctx, store.CreateUserParams{
		Email:        "admin@example.com",
		PasswordHash: "hash",
		DisplayName:  "Admin",
		Role:         store.RoleAdmin,
		MCPToken:     &token,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !user.MCPEnabled {
		t.Error("returned user has MCPEnabled = false; a token was issued, so it must be usable")
	}

	got, err := us.GetByMCPToken(ctx, token)
	if err != nil {
		t.Fatalf("GetByMCPToken on a freshly created token: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("ID = %q, want %q", got.ID, user.ID)
	}
	if !got.MCPEnabled {
		t.Error("persisted mcp_enabled = 0, want 1")
	}
}

func TestUserStore_ErrEmailTaken(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	_, err := us.Create(ctx, store.CreateUserParams{
		Email:        "dup@example.com",
		PasswordHash: "hash1",
		DisplayName:  "First",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}

	_, err = us.Create(ctx, store.CreateUserParams{
		Email:        "dup@example.com",
		PasswordHash: "hash2",
		DisplayName:  "Second",
		Role:         store.RoleMember,
	})
	if err != store.ErrEmailTaken {
		t.Fatalf("expected ErrEmailTaken, got: %v", err)
	}
}

func TestUserStore_List(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	_, err := us.Create(ctx, store.CreateUserParams{
		Email:        "u1@example.com",
		PasswordHash: "h1",
		DisplayName:  "User1",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create u1: %v", err)
	}
	_, err = us.Create(ctx, store.CreateUserParams{
		Email:        "u2@example.com",
		PasswordHash: "h2",
		DisplayName:  "User2",
		Role:         store.RoleMember,
	})
	if err != nil {
		t.Fatalf("Create u2: %v", err)
	}
	_, err = us.Create(ctx, store.CreateUserParams{
		Email:        "u3@example.com",
		PasswordHash: "h3",
		DisplayName:  "User3",
		Role:         store.RoleMember,
	})
	if err != nil {
		t.Fatalf("Create u3: %v", err)
	}

	list, err := us.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}
}

func TestUserStore_Update(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	user, err := us.Create(ctx, store.CreateUserParams{
		Email:        "update@example.com",
		PasswordHash: "hash",
		DisplayName:  "Original",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newName := "Updated"
	newRole := store.RoleMember
	trueVal := true

	// Need a second admin before we can demote this one.
	_, err = us.Create(ctx, store.CreateUserParams{
		Email:        "admin-backup@example.com",
		PasswordHash: "hash",
		DisplayName:  "BackupAdmin",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create backup admin: %v", err)
	}

	updated, err := us.Update(ctx, user.ID, store.UpdateUserParams{
		DisplayName: &newName,
		Role:        &newRole,
		MCPEnabled:  &trueVal,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if updated.DisplayName != "Updated" {
		t.Errorf("DisplayName = %q, want %q", updated.DisplayName, "Updated")
	}
	if updated.Role != store.RoleMember {
		t.Errorf("Role = %q, want %q", updated.Role, store.RoleMember)
	}
	if !updated.MCPEnabled {
		t.Error("expected MCPEnabled = true")
	}

	// Deactivate user.
	falseVal := false
	updated, err = us.Update(ctx, user.ID, store.UpdateUserParams{IsActive: &falseVal})
	if err != nil {
		t.Fatalf("Update IsActive: %v", err)
	}
	if updated.IsActive {
		t.Error("expected IsActive = false")
	}
}

func TestUserStore_UpdatePassword(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	user, err := us.Create(ctx, store.CreateUserParams{
		Email:        "pw@example.com",
		PasswordHash: "old-hash",
		DisplayName:  "PW User",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = us.UpdatePassword(ctx, user.ID, "new-hash")
	if err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}

	got, err := us.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.PasswordHash != "new-hash" {
		t.Errorf("PasswordHash = %q, want %q", got.PasswordHash, "new-hash")
	}

	// UpdatePassword for non-existent user should return ErrNotFound.
	err = us.UpdatePassword(ctx, "nonexistent-id", "x")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestUserStore_UpdateMCPToken(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	user, err := us.Create(ctx, store.CreateUserParams{
		Email:        "mcp@example.com",
		PasswordHash: "hash",
		DisplayName:  "MCP User",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = us.UpdateMCPToken(ctx, user.ID, "token-abc")
	if err != nil {
		t.Fatalf("UpdateMCPToken: %v", err)
	}

	got, err := us.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.MCPToken == nil || *got.MCPToken != "token-abc" {
		t.Errorf("MCPToken = %v, want %q", got.MCPToken, "token-abc")
	}

	// Update to a new token.
	err = us.UpdateMCPToken(ctx, user.ID, "token-xyz")
	if err != nil {
		t.Fatalf("UpdateMCPToken second: %v", err)
	}

	got, err = us.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByID second: %v", err)
	}
	if got.MCPToken == nil || *got.MCPToken != "token-xyz" {
		t.Errorf("MCPToken = %v, want %q", got.MCPToken, "token-xyz")
	}

	// UpdateMCPToken for non-existent user should return ErrNotFound.
	err = us.UpdateMCPToken(ctx, "nonexistent-id", "x")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestUserStore_Delete(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	// Create two admins so the first one can be deleted.
	user, err := us.Create(ctx, store.CreateUserParams{
		Email:        "del@example.com",
		PasswordHash: "hash",
		DisplayName:  "Deletable",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = us.Create(ctx, store.CreateUserParams{
		Email:        "keeper@example.com",
		PasswordHash: "hash",
		DisplayName:  "Keeper",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create keeper: %v", err)
	}

	err = us.Delete(ctx, user.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = us.GetByID(ctx, user.ID)
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestUserStore_Count(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	count, err := us.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	_, _ = us.Create(ctx, store.CreateUserParams{
		Email:        "c1@example.com",
		PasswordHash: "h",
		DisplayName:  "C1",
		Role:         store.RoleAdmin,
	})
	_, _ = us.Create(ctx, store.CreateUserParams{
		Email:        "c2@example.com",
		PasswordHash: "h",
		DisplayName:  "C2",
		Role:         store.RoleMember,
	})

	count, err = us.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestUserStore_LastAdminProtection(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	// Create a single admin.
	admin1, err := us.Create(ctx, store.CreateUserParams{
		Email:        "solo-admin@example.com",
		PasswordHash: "hash",
		DisplayName:  "Solo Admin",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create admin1: %v", err)
	}

	// Deleting the only admin should fail with ErrLastAdmin.
	err = us.Delete(ctx, admin1.ID)
	if err != store.ErrLastAdmin {
		t.Fatalf("expected ErrLastAdmin, got: %v", err)
	}

	// Demoting the only admin should also fail.
	memberRole := store.RoleMember
	_, err = us.Update(ctx, admin1.ID, store.UpdateUserParams{Role: &memberRole})
	if err != store.ErrLastAdmin {
		t.Fatalf("expected ErrLastAdmin on demote, got: %v", err)
	}

	// Create a second admin.
	_, err = us.Create(ctx, store.CreateUserParams{
		Email:        "admin2@example.com",
		PasswordHash: "hash",
		DisplayName:  "Admin2",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create admin2: %v", err)
	}

	// Now deleting the first admin should succeed.
	err = us.Delete(ctx, admin1.ID)
	if err != nil {
		t.Fatalf("Delete with 2 admins: %v", err)
	}

	// Verify the first admin is gone.
	_, err = us.GetByID(ctx, admin1.ID)
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestUserStore_AllowedEnvironments_Default(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	// Create with no AllowedEnvironments specified: empty slice, not nil.
	user, err := us.Create(ctx, store.CreateUserParams{
		Email:        "empty@example.com",
		PasswordHash: "h",
		DisplayName:  "Empty",
		Role:         store.RoleMember,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if user.AllowedEnvironments == nil {
		t.Fatal("AllowedEnvironments should be non-nil even when unspecified")
	}
	if len(user.AllowedEnvironments) != 0 {
		t.Errorf("AllowedEnvironments = %v, want empty slice", user.AllowedEnvironments)
	}

	// Round-trip via GetByID.
	got, err := us.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.AllowedEnvironments == nil || len(got.AllowedEnvironments) != 0 {
		t.Errorf("GetByID AllowedEnvironments = %v, want empty slice", got.AllowedEnvironments)
	}
}

func TestUserStore_AllowedEnvironments_RoundTrip(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	cases := []struct {
		name string
		envs []string
	}{
		{"single", []string{"production"}},
		{"multi", []string{"staging", "production"}},
		{"wildcard", []string{"*"}},
		{"custom_names", []string{"qa", "eu-prod", "pr-1234"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := us.Create(ctx, store.CreateUserParams{
				Email:               tc.name + "@example.com",
				PasswordHash:        "h",
				DisplayName:         tc.name,
				Role:                store.RoleMember,
				AllowedEnvironments: tc.envs,
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if !equalSlices(u.AllowedEnvironments, tc.envs) {
				t.Errorf("Create returned AllowedEnvironments = %v, want %v", u.AllowedEnvironments, tc.envs)
			}

			got, err := us.GetByID(ctx, u.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if !equalSlices(got.AllowedEnvironments, tc.envs) {
				t.Errorf("GetByID AllowedEnvironments = %v, want %v", got.AllowedEnvironments, tc.envs)
			}

			// Via List as well.
			list, err := us.List(ctx)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			found := false
			for _, row := range list {
				if row.ID == u.ID {
					if !equalSlices(row.AllowedEnvironments, tc.envs) {
						t.Errorf("List AllowedEnvironments = %v, want %v", row.AllowedEnvironments, tc.envs)
					}
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("user %q not returned from List", u.ID)
			}
		})
	}
}

func TestUserStore_AllowedEnvironments_Update(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	u, err := us.Create(ctx, store.CreateUserParams{
		Email:               "update-envs@example.com",
		PasswordHash:        "h",
		DisplayName:         "U",
		Role:                store.RoleMember,
		AllowedEnvironments: []string{"staging"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Widen scope to staging + production.
	newEnvs := []string{"staging", "production"}
	_, err = us.Update(ctx, u.ID, store.UpdateUserParams{AllowedEnvironments: &newEnvs})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := us.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !equalSlices(got.AllowedEnvironments, newEnvs) {
		t.Errorf("after widen: %v, want %v", got.AllowedEnvironments, newEnvs)
	}

	// Narrow scope back to empty (revoked).
	empty := []string{}
	_, err = us.Update(ctx, u.ID, store.UpdateUserParams{AllowedEnvironments: &empty})
	if err != nil {
		t.Fatalf("Update revoke: %v", err)
	}
	got, err = us.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID after revoke: %v", err)
	}
	if got.AllowedEnvironments == nil || len(got.AllowedEnvironments) != 0 {
		t.Errorf("after revoke: %v, want empty slice", got.AllowedEnvironments)
	}

	// Update that doesn't set AllowedEnvironments leaves it unchanged.
	displayName := "New Name"
	_, err = us.Update(ctx, u.ID, store.UpdateUserParams{DisplayName: &displayName})
	if err != nil {
		t.Fatalf("Update display name: %v", err)
	}
	got, err = us.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID after unrelated update: %v", err)
	}
	if got.DisplayName != "New Name" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "New Name")
	}
	if got.AllowedEnvironments == nil || len(got.AllowedEnvironments) != 0 {
		t.Errorf("AllowedEnvironments should still be empty: %v", got.AllowedEnvironments)
	}
}

func TestUserStore_AllowedEnvironments_BackfillMigration(t *testing.T) {
	// Ensure that the one-shot backfill in 000001_init.up.sql sets
	// allowed_environments=["*"] for pre-existing users with an MCP token.
	// We simulate the pre-multi-env state by clearing the column for a
	// token-bearing user, then re-running the backfill SQL.
	db := setupTestDB(t)
	us := NewUserStore(db)
	ctx := context.Background()

	// Seed a user with an MCP token but explicitly empty scope, as if they
	// predate the multi-env feature.
	u, err := us.Create(ctx, store.CreateUserParams{
		Email:        "legacy@example.com",
		PasswordHash: "h",
		DisplayName:  "Legacy",
		Role:         store.RoleAdmin,
		MCPToken:     ptrString("legacy-token-xyz"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Ensure legacy state: empty JSON array and mcp_token set. New users
	// default to []; simulate pre-migration by leaving it as [].
	if u.AllowedEnvironments == nil || len(u.AllowedEnvironments) != 0 {
		t.Fatalf("seed: expected [], got %v", u.AllowedEnvironments)
	}

	// Re-run the backfill statement from the migration.
	_, err = db.DB.ExecContext(ctx, `
		UPDATE users SET allowed_environments = '["*"]'
		  WHERE allowed_environments = '[]'
		    AND mcp_token IS NOT NULL`)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	got, err := us.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !equalSlices(got.AllowedEnvironments, []string{"*"}) {
		t.Errorf("after backfill: %v, want [\"*\"]", got.AllowedEnvironments)
	}

	// A tokenless user stays with [] (no backfill).
	u2, err := us.Create(ctx, store.CreateUserParams{
		Email:        "tokenless@example.com",
		PasswordHash: "h",
		DisplayName:  "Tokenless",
		Role:         store.RoleMember,
	})
	if err != nil {
		t.Fatalf("Create tokenless: %v", err)
	}
	_, err = db.DB.ExecContext(ctx, `
		UPDATE users SET allowed_environments = '["*"]'
		  WHERE allowed_environments = '[]'
		    AND mcp_token IS NOT NULL`)
	if err != nil {
		t.Fatalf("backfill 2: %v", err)
	}
	got2, err := us.GetByID(ctx, u2.ID)
	if err != nil {
		t.Fatalf("GetByID tokenless: %v", err)
	}
	if got2.AllowedEnvironments == nil || len(got2.AllowedEnvironments) != 0 {
		t.Errorf("tokenless user should keep []: %v", got2.AllowedEnvironments)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ptrString(s string) *string { return &s }
