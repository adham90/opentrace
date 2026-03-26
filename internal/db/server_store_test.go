package db

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestServerStore_RegisterAndGet(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	srv, err := ss.Register(ctx, store.RegisterServerParams{
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
	if srv.Status != store.ServerOnline {
		t.Errorf("status = %q, want %q", srv.Status, store.ServerOnline)
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
	srv1, err := ss.Register(ctx, store.RegisterServerParams{
		Hostname:     "web-01.prod",
		IPAddress:    "10.0.1.5",
		AgentVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("Register 1: %v", err)
	}

	// Second registration with same hostname should update, not duplicate
	srv2, err := ss.Register(ctx, store.RegisterServerParams{
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
	all, err := ss.List(ctx, store.ListServerParams{})
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
		if _, err := ss.Register(ctx, store.RegisterServerParams{Hostname: h}); err != nil {
			t.Fatalf("Register %s: %v", h, err)
		}
	}

	all, err := ss.List(ctx, store.ListServerParams{})
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

	srv, err := ss.Register(ctx, store.RegisterServerParams{Hostname: "hb-test"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := ss.UpdateHeartbeat(ctx, srv.ID); err != nil {
		t.Fatalf("UpdateHeartbeat: %v", err)
	}

	got, _ := ss.GetByID(ctx, srv.ID)
	if got.Status != store.ServerOnline {
		t.Errorf("status = %q, want %q", got.Status, store.ServerOnline)
	}
}

func TestServerStore_Delete(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	srv, _ := ss.Register(ctx, store.RegisterServerParams{Hostname: "del-test"})
	if err := ss.Delete(ctx, srv.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := ss.GetByID(ctx, srv.ID)
	if err != store.ErrNotFound {
		t.Errorf("GetByID after delete: got err=%v, want ErrNotFound", err)
	}
}

func TestServerStore_MarkStaleOffline(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	// Register a server
	srv, _ := ss.Register(ctx, store.RegisterServerParams{Hostname: "stale-test"})

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
	if got.Status != store.ServerOffline {
		t.Errorf("status = %q, want %q", got.Status, store.ServerOffline)
	}
}

func TestServerStore_Update(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	srv, err := ss.Register(ctx, store.RegisterServerParams{
		Hostname: "rename-test",
		OS:       "linux",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	newName := "My Production Server"
	updated, err := ss.Update(ctx, srv.ID, store.UpdateServerParams{
		DisplayName: &newName,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.DisplayName != newName {
		t.Errorf("display_name = %q, want %q", updated.DisplayName, newName)
	}
	if updated.Hostname != "rename-test" {
		t.Errorf("hostname changed to %q, want %q", updated.Hostname, "rename-test")
	}

	// Verify via GetByID
	got, err := ss.GetByID(ctx, srv.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.DisplayName != newName {
		t.Errorf("GetByID display_name = %q, want %q", got.DisplayName, newName)
	}
}

func TestServerStore_Update_NotFound(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	name := "ghost"
	_, err := ss.Update(ctx, [16]byte{}, store.UpdateServerParams{DisplayName: &name})
	if err != store.ErrNotFound {
		t.Errorf("Update nonexistent: got err=%v, want ErrNotFound", err)
	}
}

func TestServerStore_GetByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	_, err := ss.GetByID(ctx, [16]byte{})
	if err != store.ErrNotFound {
		t.Errorf("GetByID: got err=%v, want ErrNotFound", err)
	}
}

func TestServerStore_List_Pagination(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ctx := context.Background()

	// Create 5 servers with alphabetical hostnames
	for _, h := range []string{"alpha", "bravo", "charlie", "delta", "echo"} {
		if _, err := ss.Register(ctx, store.RegisterServerParams{Hostname: h}); err != nil {
			t.Fatalf("Register %s: %v", h, err)
		}
	}

	// No pagination (Limit=0) returns all
	all, err := ss.List(ctx, store.ListServerParams{})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("List all len = %d, want 5", len(all))
	}

	// Limit=2 returns first 2
	page1, err := ss.List(ctx, store.ListServerParams{Limit: 2})
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}
	if page1[0].Hostname != "alpha" || page1[1].Hostname != "bravo" {
		t.Errorf("page1 = [%s, %s], want [alpha, bravo]", page1[0].Hostname, page1[1].Hostname)
	}

	// Limit=2, Offset=2 returns next 2
	page2, err := ss.List(ctx, store.ListServerParams{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(page2))
	}
	if page2[0].Hostname != "charlie" || page2[1].Hostname != "delta" {
		t.Errorf("page2 = [%s, %s], want [charlie, delta]", page2[0].Hostname, page2[1].Hostname)
	}

	// Limit=2, Offset=4 returns last 1
	page3, err := ss.List(ctx, store.ListServerParams{Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("List page3: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3 len = %d, want 1", len(page3))
	}
	if page3[0].Hostname != "echo" {
		t.Errorf("page3[0] = %s, want echo", page3[0].Hostname)
	}

	// Offset past end returns empty
	page4, err := ss.List(ctx, store.ListServerParams{Limit: 10, Offset: 10})
	if err != nil {
		t.Fatalf("List page4: %v", err)
	}
	if len(page4) != 0 {
		t.Errorf("page4 len = %d, want 0", len(page4))
	}
}
