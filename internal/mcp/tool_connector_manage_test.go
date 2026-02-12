package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

// mockDataSourceStore implements store.DataSourceStore for testing.
type mockDataSourceStore struct {
	dataSources []store.DataSource
	created     *store.DataSource
	err         error
}

func (m *mockDataSourceStore) Create(_ context.Context, params store.CreateDataSourceParams) (*store.DataSource, error) {
	if m.err != nil {
		return nil, m.err
	}
	ds := &store.DataSource{
		ID:        uuid.New(),
		Type:      params.Type,
		Name:      params.Name,
		Config:    params.Config,
		Status:    store.StatusDisconnected,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.created = ds
	return ds, nil
}

func (m *mockDataSourceStore) GetByID(_ context.Context, id uuid.UUID) (*store.DataSource, error) {
	if m.err != nil {
		return nil, m.err
	}
	for i := range m.dataSources {
		if m.dataSources[i].ID == id {
			return &m.dataSources[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockDataSourceStore) List(_ context.Context, _ store.ListDataSourceParams) ([]store.DataSource, error) {
	return m.dataSources, m.err
}

func (m *mockDataSourceStore) Update(_ context.Context, id uuid.UUID, params store.UpdateDataSourceParams) (*store.DataSource, error) {
	for i := range m.dataSources {
		if m.dataSources[i].ID == id {
			if params.Status != nil {
				m.dataSources[i].Status = *params.Status
			}
			return &m.dataSources[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockDataSourceStore) Delete(_ context.Context, id uuid.UUID) error {
	if m.err != nil {
		return m.err
	}
	return nil
}

// --- create_connector tests ---

func TestCreateConnectorHandler_Success(t *testing.T) {
	ds := &mockDataSourceStore{}
	handler := createConnectorHandler(ds)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"name":              "Production DB",
		"type":              "database",
		"connection_string": "postgres://user:pass@localhost/db",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "Production DB") {
		t.Errorf("expected connector name in response, got: %s", text)
	}
	if !contains(text, "created") {
		t.Errorf("expected 'created' in response, got: %s", text)
	}
}

func TestCreateConnectorHandler_MissingName(t *testing.T) {
	ds := &mockDataSourceStore{}
	handler := createConnectorHandler(ds)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"type":              "database",
		"connection_string": "postgres://user:pass@localhost/db",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing name")
	}
}

func TestCreateConnectorHandler_MissingType(t *testing.T) {
	ds := &mockDataSourceStore{}
	handler := createConnectorHandler(ds)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"name":              "My DB",
		"connection_string": "postgres://user:pass@localhost/db",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing type")
	}
}

func TestCreateConnectorHandler_InvalidType(t *testing.T) {
	ds := &mockDataSourceStore{}
	handler := createConnectorHandler(ds)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"name": "My Connector",
		"type": "redis",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid type")
	}
}

func TestCreateConnectorHandler_MissingConnString(t *testing.T) {
	ds := &mockDataSourceStore{}
	handler := createConnectorHandler(ds)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"name": "My DB",
		"type": "database",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing connection_string with database type")
	}
}

func TestCreateConnectorHandler_LogsNoConnString(t *testing.T) {
	ds := &mockDataSourceStore{}
	handler := createConnectorHandler(ds)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"name": "My Logs",
		"type": "logs",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("logs connector should not require connection_string, got error: %s", resultText(t, result))
	}
}

func TestCreateConnectorHandler_StoreError(t *testing.T) {
	ds := &mockDataSourceStore{err: errors.New("db error")}
	handler := createConnectorHandler(ds)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"name":              "My DB",
		"type":              "database",
		"connection_string": "postgres://localhost/db",
	}))

	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when store fails")
	}
}

// --- delete_connector tests ---

func TestDeleteConnectorHandler_NotFound(t *testing.T) {
	ds := &mockDataSourceStore{}
	handler := deleteConnectorHandler(ds, nil)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"connector_id": uuid.New().String(),
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for non-existent connector")
	}
	text := resultText(t, result)
	if !contains(text, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", text)
	}
}

func TestDeleteConnectorHandler_MissingID(t *testing.T) {
	ds := &mockDataSourceStore{}
	handler := deleteConnectorHandler(ds, nil)

	result, err := handler(context.Background(), makeRequest(map[string]any{}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing connector_id")
	}
}
