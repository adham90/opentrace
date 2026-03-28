package connector

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/pkg/store"
)

// mockSettingsStore implements store.SettingsStore for testing resolveQueryGuardrails.
type mockSettingsStore struct {
	maxQueryRows     int
	maxQueryRowsErr  error
	stmtTimeout      int
	stmtTimeoutErr   error
}

func (m *mockSettingsStore) GetRetention(_ context.Context) (*store.RetentionSettings, error) {
	return nil, nil
}
func (m *mockSettingsStore) SetRetention(_ context.Context, _ store.RetentionSettings) error {
	return nil
}
func (m *mockSettingsStore) GetAPIKey(_ context.Context) (string, error)            { return "", nil }
func (m *mockSettingsStore) SetAPIKey(_ context.Context, _ string) error            { return nil }
func (m *mockSettingsStore) GetCORSOrigins(_ context.Context) (string, error)       { return "", nil }
func (m *mockSettingsStore) SetCORSOrigins(_ context.Context, _ string) error       { return nil }
func (m *mockSettingsStore) GetMCPName(_ context.Context) (string, error)           { return "", nil }
func (m *mockSettingsStore) SetMCPName(_ context.Context, _ string) error           { return nil }
func (m *mockSettingsStore) GetSamplingRules(_ context.Context) ([]store.SamplingRule, error) {
	return nil, nil
}
func (m *mockSettingsStore) SetSamplingRules(_ context.Context, _ []store.SamplingRule) error {
	return nil
}
func (m *mockSettingsStore) SetMaxQueryRows(_ context.Context, _ int) error    { return nil }
func (m *mockSettingsStore) SetStatementTimeout(_ context.Context, _ int) error { return nil }

func (m *mockSettingsStore) GetMaxQueryRows(_ context.Context) (int, error) {
	return m.maxQueryRows, m.maxQueryRowsErr
}

func (m *mockSettingsStore) GetStatementTimeout(_ context.Context) (int, error) {
	return m.stmtTimeout, m.stmtTimeoutErr
}

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

// --- resolveQueryGuardrails ---

func TestResolveQueryGuardrails_Defaults(t *testing.T) {
	// With nil config and nil settings store, should return defaults.
	maxRows, stmtTimeout := resolveQueryGuardrails(context.Background(), nil, nil)
	if maxRows != 500 {
		t.Errorf("default maxRows = %d, want 500", maxRows)
	}
	if stmtTimeout != 5000 {
		t.Errorf("default stmtTimeout = %d, want 5000", stmtTimeout)
	}
}

func TestResolveQueryGuardrails_FromSettingsStore(t *testing.T) {
	ss := &mockSettingsStore{
		maxQueryRows: 200,
		stmtTimeout:  3000,
	}
	maxRows, stmtTimeout := resolveQueryGuardrails(context.Background(), nil, ss)
	if maxRows != 200 {
		t.Errorf("maxRows = %d, want 200", maxRows)
	}
	if stmtTimeout != 3000 {
		t.Errorf("stmtTimeout = %d, want 3000", stmtTimeout)
	}
}

func TestResolveQueryGuardrails_SettingsStoreZeroFallsBack(t *testing.T) {
	// When settings store returns 0, should use default values.
	ss := &mockSettingsStore{
		maxQueryRows: 0,
		stmtTimeout:  0,
	}
	maxRows, stmtTimeout := resolveQueryGuardrails(context.Background(), nil, ss)
	if maxRows != 500 {
		t.Errorf("maxRows = %d, want 500 (default)", maxRows)
	}
	if stmtTimeout != 5000 {
		t.Errorf("stmtTimeout = %d, want 5000 (default)", stmtTimeout)
	}
}

func TestResolveQueryGuardrails_SettingsStoreError(t *testing.T) {
	// When settings store returns an error, should use default values.
	ss := &mockSettingsStore{
		maxQueryRowsErr: fmt.Errorf("db error"),
		stmtTimeoutErr:  fmt.Errorf("db error"),
	}
	maxRows, stmtTimeout := resolveQueryGuardrails(context.Background(), nil, ss)
	if maxRows != 500 {
		t.Errorf("maxRows = %d, want 500 (default)", maxRows)
	}
	if stmtTimeout != 5000 {
		t.Errorf("stmtTimeout = %d, want 5000 (default)", stmtTimeout)
	}
}

func TestResolveQueryGuardrails_EnvOverridesSettingsStore(t *testing.T) {
	// Set env vars to trigger env override path.
	os.Setenv("OPENTRACE_MAX_QUERY_ROWS", "1000")
	os.Setenv("OPENTRACE_STATEMENT_TIMEOUT_MS", "10000")
	t.Cleanup(func() {
		os.Unsetenv("OPENTRACE_MAX_QUERY_ROWS")
		os.Unsetenv("OPENTRACE_STATEMENT_TIMEOUT_MS")
	})

	cfg := &config.Config{
		MaxQueryRows:       1000,
		StatementTimeoutMS: 10000,
	}
	ss := &mockSettingsStore{
		maxQueryRows: 200,
		stmtTimeout:  3000,
	}

	maxRows, stmtTimeout := resolveQueryGuardrails(context.Background(), cfg, ss)
	if maxRows != 1000 {
		t.Errorf("maxRows = %d, want 1000 (from env/config)", maxRows)
	}
	if stmtTimeout != 10000 {
		t.Errorf("stmtTimeout = %d, want 10000 (from env/config)", stmtTimeout)
	}
}

func TestResolveQueryGuardrails_EnvSetButNilConfig(t *testing.T) {
	// Env is set but config is nil — should fall through to settings store.
	os.Setenv("OPENTRACE_MAX_QUERY_ROWS", "1000")
	os.Setenv("OPENTRACE_STATEMENT_TIMEOUT_MS", "10000")
	t.Cleanup(func() {
		os.Unsetenv("OPENTRACE_MAX_QUERY_ROWS")
		os.Unsetenv("OPENTRACE_STATEMENT_TIMEOUT_MS")
	})

	ss := &mockSettingsStore{
		maxQueryRows: 200,
		stmtTimeout:  3000,
	}

	maxRows, stmtTimeout := resolveQueryGuardrails(context.Background(), nil, ss)
	if maxRows != 200 {
		t.Errorf("maxRows = %d, want 200 (from settings store since config is nil)", maxRows)
	}
	if stmtTimeout != 3000 {
		t.Errorf("stmtTimeout = %d, want 3000 (from settings store since config is nil)", stmtTimeout)
	}
}

func TestResolveQueryGuardrails_PartialEnvOverride(t *testing.T) {
	// Only max rows env is set, timeout should come from settings store.
	os.Setenv("OPENTRACE_MAX_QUERY_ROWS", "800")
	t.Cleanup(func() {
		os.Unsetenv("OPENTRACE_MAX_QUERY_ROWS")
	})

	cfg := &config.Config{
		MaxQueryRows:       800,
		StatementTimeoutMS: 7000,
	}
	ss := &mockSettingsStore{
		maxQueryRows: 200,
		stmtTimeout:  3000,
	}

	maxRows, stmtTimeout := resolveQueryGuardrails(context.Background(), cfg, ss)
	if maxRows != 800 {
		t.Errorf("maxRows = %d, want 800 (from env/config)", maxRows)
	}
	if stmtTimeout != 3000 {
		t.Errorf("stmtTimeout = %d, want 3000 (from settings store since env not set)", stmtTimeout)
	}
}
