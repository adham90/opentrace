package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

// captureHandler records the arguments the wrapped handler actually received.
func captureHandler(got *map[string]any) ToolHandlerFunc {
	return func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		*got = GetArguments(req)
		return NewToolResultText("ok"), nil
	}
}

func callWith(t *testing.T, h ToolHandlerFunc, ctx context.Context, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	res, err := h(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "logs", Arguments: raw}})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	return res
}

func TestDeployWindowResolvesToken(t *testing.T) {
	ds := mocks.NewDeployStore()
	deployedAt := time.Now().Add(-90 * time.Minute).UTC().Truncate(time.Second)
	if err := ds.Record(context.Background(), store.Deploy{
		CommitHash: "abc123", Service: "api", Environment: "production", FirstSeenAt: deployedAt,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	deps := Deps{Stores: store.Stores{DeployStore: ds}}
	var got map[string]any
	h := wrapWithDeployWindow(deps, captureHandler(&got))

	ctx := envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"production"}})
	callWith(t, h, ctx, map[string]any{"action": "search", "since": DeployWindowToken})

	since, _ := got["since"].(string)
	parsed, err := time.Parse(time.RFC3339, since)
	if err != nil {
		t.Fatalf("since = %q, want an RFC3339 timestamp: %v", since, err)
	}
	if !parsed.Equal(deployedAt) {
		t.Errorf("since = %v, want the deploy time %v", parsed, deployedAt)
	}
}

// The token must work through every argument name that carries a window,
// or it silently degrades to the default on the tools using the legacy names.
func TestDeployWindowResolvesLegacyArgNames(t *testing.T) {
	for _, key := range []string{"since", "time_range", "timeframe"} {
		t.Run(key, func(t *testing.T) {
			ds := mocks.NewDeployStore()
			at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			if err := ds.Record(context.Background(), store.Deploy{
				CommitHash: "c", Environment: "production", FirstSeenAt: at,
			}); err != nil {
				t.Fatalf("Record: %v", err)
			}

			var got map[string]any
			h := wrapWithDeployWindow(Deps{Stores: store.Stores{DeployStore: ds}}, captureHandler(&got))
			ctx := envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"production"}})
			callWith(t, h, ctx, map[string]any{"action": "search", key: DeployWindowToken})

			if v, _ := got[key].(string); v == DeployWindowToken {
				t.Fatalf("%s was left unresolved", key)
			}
		})
	}
}

func TestDeployWindowPassesThroughOtherValues(t *testing.T) {
	var got map[string]any
	h := wrapWithDeployWindow(Deps{Stores: store.Stores{DeployStore: mocks.NewDeployStore()}}, captureHandler(&got))

	callWith(t, h, context.Background(), map[string]any{"action": "search", "since": "24h"})
	if got["since"] != "24h" {
		t.Errorf("since = %v, want it left alone", got["since"])
	}
}

// An env-scoped caller must get their own environment's deploy, not whichever
// one happens to be newest.
func TestDeployWindowRespectsEnvScope(t *testing.T) {
	ds := mocks.NewDeployStore()
	ctx := context.Background()
	staging := time.Now().Add(-5 * time.Hour).UTC().Truncate(time.Second)
	prod := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	for _, d := range []store.Deploy{
		{CommitHash: "s", Environment: "staging", FirstSeenAt: staging},
		{CommitHash: "p", Environment: "production", FirstSeenAt: prod},
	} {
		if err := ds.Record(ctx, d); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	var got map[string]any
	h := wrapWithDeployWindow(Deps{Stores: store.Stores{DeployStore: ds}}, captureHandler(&got))
	scoped := envscope.With(ctx, envscope.EnvScope{Allowed: []string{"staging"}})
	callWith(t, h, scoped, map[string]any{"action": "search", "since": DeployWindowToken})

	parsed, err := time.Parse(time.RFC3339, got["since"].(string))
	if err != nil {
		t.Fatalf("parsing since: %v", err)
	}
	if !parsed.Equal(staging) {
		t.Errorf("staging-scoped caller got %v, want the staging deploy %v", parsed, staging)
	}
}

// With no deploy on record the caller gets a usable error rather than a window
// that silently means "all of history".
func TestDeployWindowNoDeployRecorded(t *testing.T) {
	var got map[string]any
	h := wrapWithDeployWindow(Deps{Stores: store.Stores{DeployStore: mocks.NewDeployStore()}}, captureHandler(&got))

	ctx := envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"production"}})
	res := callWith(t, h, ctx, map[string]any{"action": "search", "since": DeployWindowToken})

	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	if got != nil {
		t.Error("handler ran despite an unresolvable window")
	}
}

func TestDeployWindowNilStore(t *testing.T) {
	var got map[string]any
	h := wrapWithDeployWindow(Deps{}, captureHandler(&got))

	res := callWith(t, h, context.Background(), map[string]any{"action": "search", "since": DeployWindowToken})
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
}
