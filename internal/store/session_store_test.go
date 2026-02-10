package store

import (
	"context"
	"testing"
	"time"
)

func TestSessionStore_CreateAndGetByToken(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ss := NewSessionStore(db)
	ctx := context.Background()

	user, err := us.Create(ctx, CreateUserParams{
		Email:        "sess@example.com",
		PasswordHash: "hash",
		DisplayName:  "Session User",
		Role:         RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}

	expiry := time.Now().Add(7 * 24 * time.Hour)
	sess, err := ss.Create(ctx, user.ID, "session-token-abc", expiry)
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	if sess.ID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if sess.UserID != user.ID {
		t.Errorf("UserID = %q, want %q", sess.UserID, user.ID)
	}
	if sess.Token != "session-token-abc" {
		t.Errorf("Token = %q, want %q", sess.Token, "session-token-abc")
	}

	// GetByToken should return the same session.
	got, err := ss.GetByToken(ctx, "session-token-abc")
	if err != nil {
		t.Fatalf("GetByToken: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("GetByToken ID = %q, want %q", got.ID, sess.ID)
	}
	if got.UserID != user.ID {
		t.Errorf("GetByToken UserID = %q, want %q", got.UserID, user.ID)
	}

	// Non-existent token should return ErrNotFound.
	_, err = ss.GetByToken(ctx, "nonexistent-token")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for bad token, got: %v", err)
	}
}

func TestSessionStore_GetByToken_Expired(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ss := NewSessionStore(db)
	ctx := context.Background()

	user, err := us.Create(ctx, CreateUserParams{
		Email:        "expired@example.com",
		PasswordHash: "hash",
		DisplayName:  "Expired User",
		Role:         RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}

	// Create a session that already expired.
	pastExpiry := time.Now().Add(-1 * time.Hour)
	_, err = ss.Create(ctx, user.ID, "expired-token", pastExpiry)
	if err != nil {
		t.Fatalf("Create expired session: %v", err)
	}

	// GetByToken should not return expired sessions.
	_, err = ss.GetByToken(ctx, "expired-token")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for expired session, got: %v", err)
	}
}

func TestSessionStore_Delete(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ss := NewSessionStore(db)
	ctx := context.Background()

	user, err := us.Create(ctx, CreateUserParams{
		Email:        "delses@example.com",
		PasswordHash: "hash",
		DisplayName:  "Delete Session",
		Role:         RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}

	expiry := time.Now().Add(7 * 24 * time.Hour)
	sess, err := ss.Create(ctx, user.ID, "delete-me-token", expiry)
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	err = ss.Delete(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = ss.GetByToken(ctx, "delete-me-token")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestSessionStore_DeleteExpired(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ss := NewSessionStore(db)
	ctx := context.Background()

	user, err := us.Create(ctx, CreateUserParams{
		Email:        "cleanup@example.com",
		PasswordHash: "hash",
		DisplayName:  "Cleanup User",
		Role:         RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}

	// Create one expired session.
	pastExpiry := time.Now().Add(-1 * time.Hour)
	_, err = ss.Create(ctx, user.ID, "expired-session", pastExpiry)
	if err != nil {
		t.Fatalf("Create expired session: %v", err)
	}

	// Create one valid session.
	futureExpiry := time.Now().Add(7 * 24 * time.Hour)
	_, err = ss.Create(ctx, user.ID, "valid-session", futureExpiry)
	if err != nil {
		t.Fatalf("Create valid session: %v", err)
	}

	// Delete expired sessions.
	deleted, err := ss.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	// The valid session should still exist.
	_, err = ss.GetByToken(ctx, "valid-session")
	if err != nil {
		t.Errorf("valid session should still exist, got: %v", err)
	}

	// The expired session should be gone (it was already gone from GetByToken
	// due to the expiry check, but now it should be physically deleted).
	// We verify by trying DeleteExpired again -- nothing more to delete.
	deleted2, err := ss.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired second: %v", err)
	}
	if deleted2 != 0 {
		t.Errorf("deleted2 = %d, want 0", deleted2)
	}
}

func TestSessionStore_DeleteAllForUser(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ss := NewSessionStore(db)
	ctx := context.Background()

	user, err := us.Create(ctx, CreateUserParams{
		Email:        "multi@example.com",
		PasswordHash: "hash",
		DisplayName:  "Multi Session",
		Role:         RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}

	expiry := time.Now().Add(7 * 24 * time.Hour)
	_, err = ss.Create(ctx, user.ID, "session-1", expiry)
	if err != nil {
		t.Fatalf("Create session 1: %v", err)
	}
	_, err = ss.Create(ctx, user.ID, "session-2", expiry)
	if err != nil {
		t.Fatalf("Create session 2: %v", err)
	}

	err = ss.DeleteAllForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("DeleteAllForUser: %v", err)
	}

	// Both sessions should be gone.
	_, err = ss.GetByToken(ctx, "session-1")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for session-1, got: %v", err)
	}
	_, err = ss.GetByToken(ctx, "session-2")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for session-2, got: %v", err)
	}
}
