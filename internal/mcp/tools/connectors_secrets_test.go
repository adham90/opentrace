package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

const (
	testDBPassword  = "sup3rs3cr3t-db-pw"
	testAuthToken   = "eyJhbGciOi-turso-auth-token"
	testFutureToken = "future-secret-value"
)

// seedSecretConnector stores a connector whose config carries every kind of
// secret the codebase writes today, plus an unknown future key.
func seedSecretConnector(t *testing.T) (*mocks.DataSourceStore, uuid.UUID) {
	t.Helper()
	ds := mocks.NewDataSourceStore()
	created, err := ds.Create(context.Background(), store.CreateDataSourceParams{
		Type: store.ConnectorDatabase,
		Name: "prod-db",
		Config: map[string]any{
			// sslmode=nope makes the connection attempt fail during DSN parsing,
			// so the "test" action never touches the network.
			"connection_string": "postgres://appuser:" + testDBPassword + "@db.internal:5432/prod?sslmode=nope",
			"auth_token":        testAuthToken,
			"some_future_key":   testFutureToken,
		},
		Environment: "production",
	})
	if err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	// The mock's Create drops Environment; set it directly so the redacted view
	// can be checked for it.
	ds.Sources[created.ID].Environment = "production"
	return ds, created.ID
}

// assertNoSecrets fails when any secret value appears in the tool output.
func assertNoSecrets(t *testing.T, text string) {
	t.Helper()
	for _, secret := range []string{testDBPassword, testAuthToken, testFutureToken} {
		if strings.Contains(text, secret) {
			t.Fatalf("tool output leaked a secret (%q):\n%s", secret, text)
		}
	}
}

func TestHandleConnectorGet_RedactsCredentials(t *testing.T) {
	dsStore, id := seedSecretConnector(t)
	d := ConnectorsDeps{DSStore: dsStore}

	result, err := HandleConnectorGet(context.Background(), d, map[string]any{
		"connector_id": id.String(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", extractText(t, result))
	}

	text := extractText(t, result)
	assertNoSecrets(t, text)

	// The useful, non-secret parts must still be there.
	for _, want := range []string{"prod-db", "production", id.String()} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in the redacted view, got:\n%s", want, text)
		}
	}
	// The raw config map must not be serialized at all.
	if strings.Contains(text, `"config"`) {
		t.Errorf("connector view must not contain a config object:\n%s", text)
	}
}

func TestHandleConnectorList_RedactsCredentials(t *testing.T) {
	dsStore, _ := seedSecretConnector(t)
	d := ConnectorsDeps{DSStore: dsStore}

	result, err := HandleConnectorList(context.Background(), d, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertNoSecrets(t, extractText(t, result))
}

func TestHandleConnectorTest_RedactsCredentialsInErrors(t *testing.T) {
	dsStore, id := seedSecretConnector(t)
	d := ConnectorsDeps{DSStore: dsStore, IsAdmin: true}

	// No real database behind the DSN, so this fails — the failure message and
	// the status message persisted on the connector must both be scrubbed.
	result, err := HandleConnectorTest(context.Background(), d, map[string]any{
		"connector_id": id.String(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertNoSecrets(t, extractText(t, result))

	stored, err := dsStore.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("get stored connector: %v", err)
	}
	if stored.StatusMessage != nil {
		assertNoSecrets(t, *stored.StatusMessage)
	}
}

// connectorErrorText must strip credentials that a driver echoed back, even
// when the driver itself does no redaction.
func TestConnectorErrorText_StripsCredentials(t *testing.T) {
	raw := errors.New("dial failed for postgres://appuser:" + testDBPassword + "@db.internal:5432/prod")
	got := connectorErrorText("connection test failed: %v", raw)
	if strings.Contains(got, testDBPassword) {
		t.Fatalf("password survived redaction: %s", got)
	}
	if !strings.Contains(got, "connection test failed") {
		t.Errorf("expected the message to be preserved, got: %s", got)
	}
}

// --- admin gating of the write actions ---

func TestConnectors_WriteActionsRequireAdmin(t *testing.T) {
	dsStore, id := seedSecretConnector(t)
	member := ConnectorsDeps{DSStore: dsStore, IsAdmin: false}

	cases := []struct {
		action string
		args   map[string]any
	}{
		{"create", map[string]any{"action": "create", "name": "x", "connector_type": "database", "connection_string": "postgres://u:p@h/d"}},
		{"update", map[string]any{"action": "update", "connector_id": id.String(), "name": "renamed"}},
		{"delete", map[string]any{"action": "delete", "connector_id": id.String()}},
		{"test", map[string]any{"action": "test", "connector_id": id.String()}},
	}

	handler := ConnectorsHandler(member)
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			result, err := handler(context.Background(), MakeCallToolRequest("connectors", tc.args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected a member to be denied action %q", tc.action)
			}
			if txt := extractText(t, result); !strings.Contains(txt, "admin") {
				t.Errorf("expected an admin-required message, got: %s", txt)
			}
		})
	}

	// The member must not have mutated anything.
	if _, err := dsStore.GetByID(context.Background(), id); err != nil {
		t.Fatalf("connector should still exist: %v", err)
	}
	all, _ := dsStore.List(context.Background(), store.ListDataSourceParams{})
	if len(all) != 1 {
		t.Fatalf("expected exactly 1 connector, got %d", len(all))
	}
	if all[0].Name != "prod-db" {
		t.Errorf("connector was renamed by a member: %s", all[0].Name)
	}
}

func TestConnectors_ReadActionsAllowedForMembers(t *testing.T) {
	dsStore, id := seedSecretConnector(t)
	member := ConnectorsDeps{DSStore: dsStore, IsAdmin: false}

	for _, args := range []map[string]any{
		{"action": "list"},
		{"action": "get", "connector_id": id.String()},
	} {
		handler := ConnectorsHandler(member)
		result, err := handler(context.Background(), MakeCallToolRequest("connectors", args))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("read action %v should be allowed for members: %s", args, extractText(t, result))
		}
		assertNoSecrets(t, extractText(t, result))
	}
}

func TestConnectorCreate_AdminAllowed(t *testing.T) {
	dsStore := mocks.NewDataSourceStore()
	admin := ConnectorsDeps{DSStore: dsStore, IsAdmin: true}

	result, err := HandleConnectorCreate(context.Background(), admin, map[string]any{
		"name":              "new-db",
		"connector_type":    "database",
		"connection_string": "postgres://u:p@h/d",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("admin create should succeed: %s", extractText(t, result))
	}
}
