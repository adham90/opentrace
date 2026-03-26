package connector

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/pkg/store"
)

func TestCreateConnector_Logs(t *testing.T) {
	ds := store.DataSource{
		Type:   store.ConnectorLogs,
		Config: map[string]any{},
	}
	c, err := CreateConnector(context.Background(), ds, &mockLogStore{}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Type() != ConnectorLogs {
		t.Fatalf("Type() = %q, want %q", c.Type(), ConnectorLogs)
	}
}

func TestCreateConnector_Database(t *testing.T) {
	// Without a real DB, we can only test that missing connection_string fails
	ds := store.DataSource{
		Type:   store.ConnectorDatabase,
		Config: map[string]any{},
	}
	_, err := CreateConnector(context.Background(), ds, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing connection_string")
	}
}

func TestCreateConnector_MySQL_MissingConnStr(t *testing.T) {
	ds := store.DataSource{
		Type:   store.ConnectorMySQL,
		Config: map[string]any{},
	}
	_, err := CreateConnector(context.Background(), ds, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing connection_string")
	}
}

func TestCreateConnector_MySQL_EmptyConnStr(t *testing.T) {
	ds := store.DataSource{
		Type:   store.ConnectorMySQL,
		Config: map[string]any{"connection_string": ""},
	}
	_, err := CreateConnector(context.Background(), ds, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty connection_string")
	}
}

func TestCreateConnector_Redis_MissingConnStr(t *testing.T) {
	ds := store.DataSource{
		Type:   store.ConnectorRedis,
		Config: map[string]any{},
	}
	_, err := CreateConnector(context.Background(), ds, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing connection_string")
	}
}

func TestCreateConnector_Redis_EmptyConnStr(t *testing.T) {
	ds := store.DataSource{
		Type:   store.ConnectorRedis,
		Config: map[string]any{"connection_string": ""},
	}
	_, err := CreateConnector(context.Background(), ds, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty connection_string")
	}
}

func TestCreateConnector_Turso_MissingConnStr(t *testing.T) {
	ds := store.DataSource{
		Type:   store.ConnectorTurso,
		Config: map[string]any{},
	}
	_, err := CreateConnector(context.Background(), ds, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing connection_string")
	}
}

func TestCreateConnector_Turso_EmptyConnStr(t *testing.T) {
	ds := store.DataSource{
		Type:   store.ConnectorTurso,
		Config: map[string]any{"connection_string": ""},
	}
	_, err := CreateConnector(context.Background(), ds, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty connection_string")
	}
}

func TestCreateConnector_Unknown(t *testing.T) {
	ds := store.DataSource{
		Type:   "unknown_type",
		Config: map[string]any{},
	}
	_, err := CreateConnector(context.Background(), ds, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}
