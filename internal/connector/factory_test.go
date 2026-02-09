package connector

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/internal/store"
)

func TestCreateConnector_Logs(t *testing.T) {
	ds := store.DataSource{
		Type:   store.ConnectorLogs,
		Config: map[string]any{},
	}
	c, err := CreateConnector(context.Background(), ds, &mockLogStore{}, nil)
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
	_, err := CreateConnector(context.Background(), ds, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing connection_string")
	}
}

func TestCreateConnector_Unknown(t *testing.T) {
	ds := store.DataSource{
		Type:   "unknown_type",
		Config: map[string]any{},
	}
	_, err := CreateConnector(context.Background(), ds, nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}
