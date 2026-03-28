package connector

import (
	"testing"
)

func TestRegistry_CloseAll(t *testing.T) {
	r := NewRegistry()

	ds1 := &mockDataSource{connType: ConnectorLogs}
	ds2 := &mockDataSource{connType: ConnectorDatabase}
	ds3 := &mockDataSource{connType: ConnectorMySQL}

	r.Register(ds1)
	r.Register(ds2)
	r.Register(ds3)

	r.CloseAll()

	if !ds1.closed.Load() {
		t.Error("logs connector should have been closed")
	}
	if !ds2.closed.Load() {
		t.Error("database connector should have been closed")
	}
	if !ds3.closed.Load() {
		t.Error("mysql connector should have been closed")
	}

	// Registry should be empty after CloseAll
	if r.Get(ConnectorLogs) != nil {
		t.Error("logs connector should be removed after CloseAll")
	}
	if r.Get(ConnectorDatabase) != nil {
		t.Error("database connector should be removed after CloseAll")
	}
	if r.Get(ConnectorMySQL) != nil {
		t.Error("mysql connector should be removed after CloseAll")
	}

	// AllTools should return empty slice
	tools := r.AllTools()
	if len(tools) != 0 {
		t.Errorf("AllTools() after CloseAll returned %d tools, want 0", len(tools))
	}
}

func TestRegistry_CloseAll_Empty(t *testing.T) {
	r := NewRegistry()
	// Should not panic on empty registry
	r.CloseAll()

	if len(r.AllTools()) != 0 {
		t.Error("expected no tools after CloseAll on empty registry")
	}
}

func TestRegistry_HasDataConnectors(t *testing.T) {
	tests := []struct {
		name       string
		connectors []ConnectorType
		want       bool
	}{
		{
			name:       "empty registry",
			connectors: nil,
			want:       false,
		},
		{
			name:       "only logs connector",
			connectors: []ConnectorType{ConnectorLogs},
			want:       true,
		},
		{
			name:       "logs and database connectors",
			connectors: []ConnectorType{ConnectorLogs, ConnectorDatabase},
			want:       true,
		},
		{
			name:       "only database connector",
			connectors: []ConnectorType{ConnectorDatabase},
			want:       true,
		},
		{
			name:       "multiple non-logs connectors",
			connectors: []ConnectorType{ConnectorMySQL, ConnectorRedis, ConnectorTurso},
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			for _, ct := range tt.connectors {
				r.Register(&mockDataSource{connType: ct})
			}
			got := r.HasDataConnectors()
			if got != tt.want {
				t.Errorf("HasDataConnectors() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegistry_HasDataConnectors_AfterUnregister(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockDataSource{connType: ConnectorDatabase})

	if !r.HasDataConnectors() {
		t.Fatal("expected true after registering database connector")
	}

	r.Unregister(ConnectorDatabase)

	if r.HasDataConnectors() {
		t.Fatal("expected false after unregistering the only connector")
	}
}

func TestRegistry_HasDataConnectors_AfterCloseAll(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockDataSource{connType: ConnectorLogs})
	r.Register(&mockDataSource{connType: ConnectorDatabase})

	if !r.HasDataConnectors() {
		t.Fatal("expected true before CloseAll")
	}

	r.CloseAll()

	if r.HasDataConnectors() {
		t.Fatal("expected false after CloseAll")
	}
}
