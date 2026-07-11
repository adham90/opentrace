package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/mcp/envscope"
)

// fakeDBConnector implements DataSource + QueryExecutor + ControlExecutor +
// EnvironmentScoped so the database tool tests can run without a live Postgres.
type fakeDBConnector struct {
	env           string
	readRows      [][]any
	controlCalled bool
}

func (f *fakeDBConnector) Type() connector.ConnectorType          { return connector.ConnectorDatabase }
func (f *fakeDBConnector) TestConnection(_ context.Context) error { return nil }
func (f *fakeDBConnector) Tools() []connector.Tool                { return nil }
func (f *fakeDBConnector) Close() error                           { return nil }
func (f *fakeDBConnector) Environment() string                    { return f.env }

func (f *fakeDBConnector) ExecuteReadQuery(_ context.Context, _ string) (*connector.QueryResult, error) {
	return &connector.QueryResult{
		Columns:  []string{"pid", "state", "app_name", "query_preview", "duration_seconds"},
		Rows:     f.readRows,
		RowCount: len(f.readRows),
	}, nil
}

func (f *fakeDBConnector) ExecuteControlQuery(_ context.Context, _ string) (*connector.QueryResult, error) {
	f.controlCalled = true
	return &connector.QueryResult{Columns: []string{"ok"}, Rows: [][]any{{true}}, RowCount: 1}, nil
}

func registryWith(ds connector.DataSource) *connector.Registry {
	r := connector.NewRegistry()
	r.Register(ds)
	return r
}

// --- Finding #2: kill_query requires admin ---

func TestKillQuery_MemberDenied(t *testing.T) {
	deps := DatabaseDeps{Registry: connector.NewRegistry(), IsAdmin: false}
	result, err := HandleKillQuery(context.Background(), deps, map[string]any{"pid": float64(123)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected member to be denied kill_query")
	}
	if txt := extractText(t, result); !strings.Contains(txt, "admin") {
		t.Errorf("expected an admin-required message, got: %s", txt)
	}
}

func TestKillQuery_AdminAllowed(t *testing.T) {
	fake := &fakeDBConnector{env: "", readRows: [][]any{{123, "active", "app", "SELECT 1", 10}}}
	deps := DatabaseDeps{Registry: registryWith(fake), IsAdmin: true}

	result, err := HandleKillQuery(context.Background(), deps, map[string]any{"pid": float64(123)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("admin kill_query should succeed, got: %s", extractText(t, result))
	}
	if !fake.controlCalled {
		t.Error("expected kill_query to route through the privileged control executor")
	}
}

// --- Finding #5: connector env scope enforced at query time ---

func TestDatabaseTool_RejectsCrossEnvConnector(t *testing.T) {
	fake := &fakeDBConnector{env: "production"}
	deps := DatabaseDeps{Registry: registryWith(fake)}
	ctx := envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"staging"}})

	result, err := HandleQueries(ctx, deps, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected a staging token to be denied a production-pinned connector")
	}
	if txt := extractText(t, result); !strings.Contains(txt, "environment") {
		t.Errorf("expected an env-scope denial, got: %s", txt)
	}
}

func TestDatabaseTool_AllowsInScopeConnector(t *testing.T) {
	fake := &fakeDBConnector{env: "staging"}
	deps := DatabaseDeps{Registry: registryWith(fake)}
	ctx := envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"staging"}})

	// getQueryExecutor must pass the env check; HandleQueries then runs the
	// pg_stat_statements query against the fake and returns a normal result.
	result, err := HandleQueries(ctx, deps, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("in-scope staging token should be allowed, got: %s", extractText(t, result))
	}
}

func TestDatabaseTool_SharedConnectorAlwaysAllowed(t *testing.T) {
	// A "*"-env (shared) connector is queryable from any scope.
	fake := &fakeDBConnector{env: envscope.WildcardEnv}
	deps := DatabaseDeps{Registry: registryWith(fake)}
	ctx := envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"staging"}})

	result, err := HandleQueries(ctx, deps, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("shared connector should be allowed for any scope, got: %s", extractText(t, result))
	}
}
