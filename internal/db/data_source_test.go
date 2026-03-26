package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/pkg/store"
)

func TestCreateDataSource(t *testing.T) {
	db := setupTestDB(t)
	s := NewDataSourceStore(db)
	ctx := context.Background()

	ds, err := s.Create(ctx, store.CreateDataSourceParams{
		Type:   store.ConnectorLogs,
		Name:   "Test Logs",
		Config: map[string]any{"endpoint": "http://localhost:9200"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if ds.ID == uuid.Nil {
		t.Error("expected non-nil UUID")
	}
	if ds.Type != store.ConnectorLogs {
		t.Errorf("Type = %q, want %q", ds.Type, store.ConnectorLogs)
	}
	if ds.Name != "Test Logs" {
		t.Errorf("Name = %q, want %q", ds.Name, "Test Logs")
	}
	if ds.Status != store.StatusDisconnected {
		t.Errorf("Status = %q, want %q", ds.Status, store.StatusDisconnected)
	}
	if ds.Config["endpoint"] != "http://localhost:9200" {
		t.Errorf("Config[endpoint] = %v", ds.Config["endpoint"])
	}
	if ds.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if ds.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}
}

func TestGetDataSourceByID(t *testing.T) {
	db := setupTestDB(t)
	s := NewDataSourceStore(db)
	ctx := context.Background()

	created, err := s.Create(ctx, store.CreateDataSourceParams{
		Type:   store.ConnectorDatabase,
		Name:   "Prod DB",
		Config: map[string]any{"host": "db.example.com"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := s.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}

	if got.ID != created.ID {
		t.Errorf("ID = %v, want %v", got.ID, created.ID)
	}
	if got.Name != "Prod DB" {
		t.Errorf("Name = %q, want %q", got.Name, "Prod DB")
	}
}

func TestGetByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	s := NewDataSourceStore(db)
	ctx := context.Background()

	_, err := s.GetByID(ctx, uuid.New())
	if err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestListDataSources(t *testing.T) {
	db := setupTestDB(t)
	s := NewDataSourceStore(db)
	ctx := context.Background()

	s.Create(ctx, store.CreateDataSourceParams{Type: store.ConnectorLogs, Name: "Logs", Config: map[string]any{}})
	s.Create(ctx, store.CreateDataSourceParams{Type: store.ConnectorDatabase, Name: "DB", Config: map[string]any{}})

	list, err := s.List(ctx, store.ListDataSourceParams{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
}

func TestListDataSources_Empty(t *testing.T) {
	db := setupTestDB(t)
	s := NewDataSourceStore(db)
	ctx := context.Background()

	list, err := s.List(ctx, store.ListDataSourceParams{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if list == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(list) != 0 {
		t.Fatalf("len = %d, want 0", len(list))
	}
}

func TestUpdateDataSource_Status(t *testing.T) {
	db := setupTestDB(t)
	s := NewDataSourceStore(db)
	ctx := context.Background()

	created, err := s.Create(ctx, store.CreateDataSourceParams{
		Type:   store.ConnectorLogs,
		Name:   "Logs",
		Config: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	status := store.StatusConnected
	now := time.Now()
	updated, err := s.Update(ctx, created.ID, store.UpdateDataSourceParams{
		Status:       &status,
		LastTestedAt: &now,
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if updated.Status != store.StatusConnected {
		t.Errorf("Status = %q, want %q", updated.Status, store.StatusConnected)
	}
}

func TestUpdateDataSource_Name(t *testing.T) {
	db := setupTestDB(t)
	s := NewDataSourceStore(db)
	ctx := context.Background()

	created, err := s.Create(ctx, store.CreateDataSourceParams{
		Type:   store.ConnectorDatabase,
		Name:   "Old Name",
		Config: map[string]any{"host": "db.example.com"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	newName := "New Name"
	updated, err := s.Update(ctx, created.ID, store.UpdateDataSourceParams{
		Name: &newName,
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if updated.Name != "New Name" {
		t.Errorf("Name = %q, want %q", updated.Name, "New Name")
	}
	// Status should remain unchanged when only name is updated
	if updated.Status != store.StatusDisconnected {
		t.Errorf("Status = %q, want %q", updated.Status, store.StatusDisconnected)
	}
}

func TestUpdateDataSource_Config(t *testing.T) {
	db := setupTestDB(t)
	s := NewDataSourceStore(db)
	ctx := context.Background()

	// First create and mark as connected
	created, err := s.Create(ctx, store.CreateDataSourceParams{
		Type:   store.ConnectorDatabase,
		Name:   "DB",
		Config: map[string]any{"connection_string": "postgres://old"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	status := store.StatusConnected
	now := time.Now()
	_, err = s.Update(ctx, created.ID, store.UpdateDataSourceParams{
		Status: &status, LastTestedAt: &now,
	})
	if err != nil {
		t.Fatalf("Update status failed: %v", err)
	}

	// Now update config — status should reset to disconnected
	newConfig := map[string]any{"connection_string": "postgres://new"}
	updated, err := s.Update(ctx, created.ID, store.UpdateDataSourceParams{
		Config: newConfig,
	})
	if err != nil {
		t.Fatalf("Update config failed: %v", err)
	}

	if updated.Config["connection_string"] != "postgres://new" {
		t.Errorf("Config = %v, want connection_string=postgres://new", updated.Config)
	}
	if updated.Status != store.StatusDisconnected {
		t.Errorf("Status = %q, want %q (should reset on config change)", updated.Status, store.StatusDisconnected)
	}
}

func TestUpdateDataSource_NameAndConfig(t *testing.T) {
	db := setupTestDB(t)
	s := NewDataSourceStore(db)
	ctx := context.Background()

	created, err := s.Create(ctx, store.CreateDataSourceParams{
		Type:   store.ConnectorDatabase,
		Name:   "Old",
		Config: map[string]any{"host": "old.example.com"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	newName := "New"
	newConfig := map[string]any{"host": "new.example.com"}
	updated, err := s.Update(ctx, created.ID, store.UpdateDataSourceParams{
		Name:   &newName,
		Config: newConfig,
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if updated.Name != "New" {
		t.Errorf("Name = %q, want %q", updated.Name, "New")
	}
	if updated.Config["host"] != "new.example.com" {
		t.Errorf("Config[host] = %v, want new.example.com", updated.Config["host"])
	}
}

func TestDeleteDataSource(t *testing.T) {
	db := setupTestDB(t)
	s := NewDataSourceStore(db)
	ctx := context.Background()

	created, _ := s.Create(ctx, store.CreateDataSourceParams{
		Type:   store.ConnectorLogs,
		Name:   "Logs",
		Config: map[string]any{},
	})

	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err := s.GetByID(ctx, created.ID)
	if err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	db := setupTestDB(t)
	s := NewDataSourceStore(db)
	ctx := context.Background()

	err := s.Delete(ctx, uuid.New())
	if err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}
