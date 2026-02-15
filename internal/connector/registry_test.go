package connector

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

)

type mockDataSource struct {
	connType ConnectorType
	tools    []Tool
	closed   atomic.Bool
}

func (m *mockDataSource) Type() ConnectorType                      { return m.connType }
func (m *mockDataSource) TestConnection(ctx context.Context) error { return nil }
func (m *mockDataSource) Tools() []Tool                     { return m.tools }
func (m *mockDataSource) Close() error {
	m.closed.Store(true)
	return nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	ds := &mockDataSource{connType: ConnectorLogs}

	r.Register(ds)

	got := r.Get(ConnectorLogs)
	if got != ds {
		t.Fatal("expected registered datasource")
	}

	// Non-existent type returns nil
	if r.Get(ConnectorDatabase) != nil {
		t.Fatal("expected nil for unregistered type")
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	old := &mockDataSource{connType: ConnectorLogs}
	new := &mockDataSource{connType: ConnectorLogs}

	r.Register(old)
	r.Register(new)

	if !old.closed.Load() {
		t.Fatal("old connector should have been closed")
	}
	if r.Get(ConnectorLogs) != new {
		t.Fatal("should return new connector")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	ds := &mockDataSource{connType: ConnectorLogs}
	r.Register(ds)

	removed := r.Unregister(ConnectorLogs)
	if !removed {
		t.Fatal("expected true for existing connector")
	}
	if !ds.closed.Load() {
		t.Fatal("connector should have been closed")
	}
	if r.Get(ConnectorLogs) != nil {
		t.Fatal("expected nil after unregister")
	}
}

func TestUnregister_NonExistent(t *testing.T) {
	r := NewRegistry()
	removed := r.Unregister(ConnectorLogs)
	if removed {
		t.Fatal("expected false for non-existent connector")
	}
}

func TestRegistry_AllTools(t *testing.T) {
	r := NewRegistry()

	logsTool := Tool{Name: "log_search", Description: "Search logs"}
	dbTool := Tool{Name: "db_search", Description: "Search database"}

	r.Register(&mockDataSource{
		connType: ConnectorLogs,
		tools:    []Tool{logsTool},
	})
	r.Register(&mockDataSource{
		connType: ConnectorDatabase,
		tools:    []Tool{dbTool},
	})

	tools := r.AllTools()
	if len(tools) != 2 {
		t.Fatalf("len = %d, want 2", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["log_search"] || !names["db_search"] {
		t.Errorf("tools = %v, want log_search and db_search", names)
	}
}

func TestRegistry_AllTools_Empty(t *testing.T) {
	r := NewRegistry()
	tools := r.AllTools()

	if tools == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(tools) != 0 {
		t.Fatalf("len = %d, want 0", len(tools))
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup

	// Concurrent registrations
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Register(&mockDataSource{
				connType: ConnectorLogs,
				tools:    []Tool{{Name: "log_search"}},
			})
		}()
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.AllTools()
			r.Get(ConnectorLogs)
		}()
	}

	// Concurrent unregistrations
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Unregister(ConnectorLogs)
		}()
	}

	wg.Wait()
}
