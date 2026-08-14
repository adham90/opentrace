package mcp

import (
	"context"
	"regexp"
	"slices"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/store"
)

// toolActionRe matches the tool/action pairs the prompt bodies tell the agent
// to call: opentrace(tool="x", action="y", ...).
var toolActionRe = regexp.MustCompile(`tool="([a-z_]+)",\s*action="([a-z_]+)"`)

func promptTexts(t *testing.T) map[string]string {
	t.Helper()
	cases := []struct {
		name    string
		handler func(context.Context, *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error)
		args    map[string]string
	}{
		{"investigate-errors", investigateErrorsHandler, map[string]string{"service": "api"}},
		{"database-health-check", databaseHealthCheckHandler, nil},
		{"deploy-validation", deployValidationHandler, map[string]string{"service": "api"}},
		{"triage", triageHandler, nil},
	}
	out := make(map[string]string, len(cases))
	for _, c := range cases {
		req := &mcpsdk.GetPromptRequest{Params: &mcpsdk.GetPromptParams{Arguments: c.args}}
		res, err := c.handler(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		for _, m := range res.Messages {
			if tc, ok := m.Content.(*mcpsdk.TextContent); ok {
				out[c.name] += tc.Text
			}
		}
	}
	return out
}

// TestPrompts_ReferenceOnlyRegisteredToolsAndActions — every guided workflow
// must be executable: no unknown tool, no unknown action.
func TestPrompts_ReferenceOnlyRegisteredToolsAndActions(t *testing.T) {
	deps := Deps{
		DB:       setupTestDB(t).DB,
		Registry: connector.NewRegistry(),
		Stores: store.Stores{
			LogStore:         newPromptTestLogStore(),
			ErrorGroupStore:  newPromptTestErrorStore(),
			HealthCheckStore: newPromptTestHealthStore(),
			WatchStore:       newPromptTestWatchStore(),
		},
	}
	gw := buildGateway(deps, true, &CatalogBuilder{})

	actionsByTool := make(map[string][]string, len(gw.entries))
	for _, e := range gw.entries {
		actionsByTool[e.Name] = e.Actions
	}

	for name, text := range promptTexts(t) {
		for _, m := range toolActionRe.FindAllStringSubmatch(text, -1) {
			tool, action := m[1], m[2]
			actions, ok := actionsByTool[tool]
			if !ok {
				t.Errorf("prompt %q calls unregistered tool %q", name, tool)
				continue
			}
			if !slices.Contains(actions, action) {
				t.Errorf("prompt %q calls %s/%s which is not a registered action (have %v)", name, tool, action, actions)
			}
		}
	}
}

// --- minimal stores so the tools under test get registered ---

type promptTestLogStore struct{ store.LogStore }
type promptTestErrorStore struct{ store.ErrorGroupStore }
type promptTestHealthStore struct{ store.HealthCheckStore }
type promptTestWatchStore struct{ store.WatchStore }

func newPromptTestLogStore() store.LogStore          { return &promptTestLogStore{} }
func newPromptTestErrorStore() store.ErrorGroupStore { return &promptTestErrorStore{} }
func newPromptTestHealthStore() store.HealthCheckStore {
	return &promptTestHealthStore{}
}
func newPromptTestWatchStore() store.WatchStore { return &promptTestWatchStore{} }

// --- catalog ---

// connectorToolDataSource is a connector exposing one dynamic tool.
type connectorToolDataSource struct{}

func (connectorToolDataSource) Type() connector.ConnectorType        { return "postgres" }
func (connectorToolDataSource) TestConnection(context.Context) error { return nil }
func (connectorToolDataSource) Close() error                         { return nil }
func (connectorToolDataSource) Tools() []connector.Tool {
	return []connector.Tool{{Name: "pg_slow_queries", Description: "Slow queries from postgres"}}
}

// TestBuildCatalog_IncludesDynamicConnectorTools — dynamic connector tools are
// registered in the gateway and must also show up on the web /tools page.
func TestBuildCatalog_IncludesDynamicConnectorTools(t *testing.T) {
	reg := connector.NewRegistry()
	reg.Register(connectorToolDataSource{})

	catalog := BuildCatalog(Deps{Registry: reg})

	found := false
	for _, cat := range catalog.Categories() {
		if cat.Name != "Connector Queries" {
			continue
		}
		if cat.Description == "" {
			t.Error("Connector Queries category has no description")
		}
		for _, tool := range cat.Tools {
			if tool.Name == "pg_slow_queries" {
				found = true
			}
		}
	}
	if !found {
		t.Error("dynamic connector tool missing from the web catalog")
	}
}

// TestCatalogCategories_AllHaveDescriptions — the catalog and the registration
// code must not drift apart again.
func TestCatalogCategories_AllHaveDescriptions(t *testing.T) {
	catalog := BuildCatalog(Deps{Registry: connector.NewRegistry()})
	for _, cat := range catalog.Categories() {
		if cat.Description == "" {
			t.Errorf("category %q has no description", cat.Name)
		}
	}
}

// --- activity logger lifecycle ---

// TestActivityLogger_ClosesWhenLifecycleCtxEnds covers the leak: loggers
// created inside NewConfiguredServer must shut down with the app context.
func TestActivityLogger_ClosesWhenLifecycleCtxEnds(t *testing.T) {
	capStore := &capturingActivityStore{}
	ctx, cancel := context.WithCancel(context.Background())
	al := NewActivityLogger(ctx, capStore, 8, 1)
	al.closeOnDone(ctx)

	al.Log(store.LogMCPActivityParams{ToolName: "logs"})
	cancel()

	deadline := time.Now().Add(2 * time.Second)
	for {
		al.mu.RLock()
		closed := al.closed
		al.mu.RUnlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("logger was not closed after its lifecycle ctx ended")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Buffered entries are drained, not dropped, even though ctx is cancelled.
	if len(capStore.all()) != 1 {
		t.Errorf("drained %d entries, want 1", len(capStore.all()))
	}

	// Close is idempotent and Log after Close must not panic.
	al.Close()
	al.Log(store.LogMCPActivityParams{ToolName: "errors"})
}
