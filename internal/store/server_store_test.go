package store

import (
	"context"
	"testing"
	"time"
)

func TestServerStore_RegisterAndGet(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	srv, err := ss.Register(ctx, RegisterServerParams{
		Hostname:     "web-01.prod",
		IPAddress:    "10.0.1.5",
		OS:           "linux",
		Arch:         "amd64",
		AgentVersion: "0.1.0",
		Labels:       map[string]string{"env": "production"},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if srv.Hostname != "web-01.prod" {
		t.Errorf("hostname = %q, want %q", srv.Hostname, "web-01.prod")
	}
	if srv.Status != ServerOnline {
		t.Errorf("status = %q, want %q", srv.Status, ServerOnline)
	}
	if srv.Labels["env"] != "production" {
		t.Errorf("labels[env] = %q, want %q", srv.Labels["env"], "production")
	}

	// GetByID
	got, err := ss.GetByID(ctx, srv.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Hostname != srv.Hostname {
		t.Errorf("GetByID hostname = %q, want %q", got.Hostname, srv.Hostname)
	}
}

func TestServerStore_RegisterUpsert(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	// First registration
	srv1, err := ss.Register(ctx, RegisterServerParams{
		Hostname:     "web-01.prod",
		IPAddress:    "10.0.1.5",
		AgentVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("Register 1: %v", err)
	}

	// Second registration with same hostname should update, not duplicate
	srv2, err := ss.Register(ctx, RegisterServerParams{
		Hostname:     "web-01.prod",
		IPAddress:    "10.0.1.6",
		AgentVersion: "0.2.0",
	})
	if err != nil {
		t.Fatalf("Register 2: %v", err)
	}

	if srv1.ID != srv2.ID {
		t.Error("upsert should return same ID")
	}
	if srv2.IPAddress != "10.0.1.6" {
		t.Errorf("ip_address = %q, want %q", srv2.IPAddress, "10.0.1.6")
	}
	if srv2.AgentVersion != "0.2.0" {
		t.Errorf("agent_version = %q, want %q", srv2.AgentVersion, "0.2.0")
	}

	// List should have exactly one server
	all, err := ss.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("List len = %d, want 1", len(all))
	}
}

func TestServerStore_List(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	for _, h := range []string{"alpha", "beta", "gamma"} {
		if _, err := ss.Register(ctx, RegisterServerParams{Hostname: h}); err != nil {
			t.Fatalf("Register %s: %v", h, err)
		}
	}

	all, err := ss.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("List len = %d, want 3", len(all))
	}
	// Sorted by hostname ASC
	if all[0].Hostname != "alpha" {
		t.Errorf("first hostname = %q, want %q", all[0].Hostname, "alpha")
	}
}

func TestServerStore_UpdateHeartbeat(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	srv, err := ss.Register(ctx, RegisterServerParams{Hostname: "hb-test"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := ss.UpdateHeartbeat(ctx, srv.ID); err != nil {
		t.Fatalf("UpdateHeartbeat: %v", err)
	}

	got, _ := ss.GetByID(ctx, srv.ID)
	if got.Status != ServerOnline {
		t.Errorf("status = %q, want %q", got.Status, ServerOnline)
	}
}

func TestServerStore_Delete(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	srv, _ := ss.Register(ctx, RegisterServerParams{Hostname: "del-test"})
	if err := ss.Delete(ctx, srv.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := ss.GetByID(ctx, srv.ID)
	if err != ErrNotFound {
		t.Errorf("GetByID after delete: got err=%v, want ErrNotFound", err)
	}
}

func TestServerStore_MarkStaleOffline(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	// Register a server
	srv, _ := ss.Register(ctx, RegisterServerParams{Hostname: "stale-test"})

	// Manually backdate its last_seen_at to 5 minutes ago
	fiveMinAgo := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
	db.ExecContext(ctx, `UPDATE servers SET last_seen_at = ? WHERE id = ?`, fiveMinAgo, srv.ID.String())

	// Mark stale with 2-minute threshold
	n, err := ss.MarkStaleOffline(ctx, 2*time.Minute)
	if err != nil {
		t.Fatalf("MarkStaleOffline: %v", err)
	}
	if n != 1 {
		t.Errorf("marked = %d, want 1", n)
	}

	got, _ := ss.GetByID(ctx, srv.ID)
	if got.Status != ServerOffline {
		t.Errorf("status = %q, want %q", got.Status, ServerOffline)
	}
}

func TestServerStore_GetByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	_, err := ss.GetByID(ctx, [16]byte{})
	if err != ErrNotFound {
		t.Errorf("GetByID: got err=%v, want ErrNotFound", err)
	}
}
