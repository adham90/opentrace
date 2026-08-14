package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/internal/testutil/mocks"
	srvpkg "github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

// roleDeps builds the smallest dep set that registers every tool carrying
// admin-only actions (connectors, healthchecks, deep_capture).
func roleDeps(t *testing.T) Deps {
	t.Helper()
	bunDB := setupTestDB(t)
	return Deps{
		DB: bunDB.DB,
		Stores: store.Stores{
			DSStore:          mocks.NewDataSourceStore(),
			LogStore:         mocks.NewLogStore(),
			HealthCheckStore: mocks.NewHealthCheckStore(),
			SettingsStore:    mocks.NewSettingsStore(),
		},
	}
}

func callGateway(t *testing.T, gw *Gateway, tool, action string) *mcpsdk.CallToolResult {
	t.Helper()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool":   tool,
		"action": action,
		"params": map[string]any{},
	})
	res, err := gw.Handler()(context.Background(), req)
	if err != nil {
		t.Fatalf("%s/%s: unexpected error: %v", tool, action, err)
	}
	return res
}

func entryFor(t *testing.T, gw *Gateway, name string) GatewayEntry {
	t.Helper()
	for _, e := range gw.entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("tool %q not registered", name)
	return GatewayEntry{}
}

// TestMemberServer_DoesNotExposeWriteActions is the member/admin boundary test:
// the read-only server must neither advertise nor execute the admin-only
// actions of connectors, deep_capture and healthchecks.
func TestMemberServer_DoesNotExposeWriteActions(t *testing.T) {
	deps := roleDeps(t)
	member := buildGateway(deps, false, &CatalogBuilder{})

	for tool, denied := range adminOnlyActions {
		entry := entryFor(t, member, tool)
		for _, action := range denied {
			if slices.Contains(entry.Actions, action) {
				t.Errorf("member server advertises %s/%s", tool, action)
			}
			res := callGateway(t, member, tool, action)
			if res == nil || !res.IsError {
				t.Errorf("member server executed %s/%s instead of refusing it", tool, action)
				continue
			}
			text := res.Content[0].(*mcpsdk.TextContent).Text
			if !strings.Contains(text, "requires an admin token") {
				t.Errorf("%s/%s: unexpected refusal message %q", tool, action, text)
			}
		}
		if entry.Destructive {
			t.Errorf("member entry %q should not be marked destructive", tool)
		}
	}
}

// TestAdminServer_StillExposesWriteActions guards against over-restricting.
func TestAdminServer_StillExposesWriteActions(t *testing.T) {
	deps := roleDeps(t)
	admin := buildGateway(deps, true, &CatalogBuilder{})

	for tool, denied := range adminOnlyActions {
		entry := entryFor(t, admin, tool)
		for _, action := range denied {
			if !slices.Contains(entry.Actions, action) {
				t.Errorf("admin server dropped %s/%s", tool, action)
			}
		}
	}

	// And the refusal wrapper must not be in the way.
	res := callGateway(t, admin, "connectors", "create")
	if res != nil && res.IsError {
		if text := res.Content[0].(*mcpsdk.TextContent).Text; strings.Contains(text, "requires an admin token") {
			t.Error("admin server refused connectors/create as non-admin")
		}
	}
}

// TestMemberCatalog_MatchesRegistration — the member catalog description must
// not advertise the admin-only actions either.
func TestMemberCatalog_MatchesRegistration(t *testing.T) {
	deps := roleDeps(t)
	b := &CatalogBuilder{}
	buildGateway(deps, false, b)
	for _, e := range b.entries {
		if e.Name != "deep_capture" {
			continue
		}
		if strings.Contains(e.Description, "update_pii_config") || strings.Contains(e.Description, "PII config") {
			t.Errorf("member catalog advertises admin-only deep_capture actions: %q", e.Description)
		}
	}
}

// TestGatewayTool_DestructiveHintIsTruthful — the single advertised tool must
// not claim to be non-destructive when it proxies destructive actions.
func TestGatewayTool_DestructiveHintIsTruthful(t *testing.T) {
	gw := NewGateway()
	gw.Register("safe", nil, GatewayEntry{ReadOnly: true})
	tool := gw.Tool()
	if *tool.Annotations.DestructiveHint {
		t.Error("read-only gateway should not advertise destructiveHint=true")
	}
	if !tool.Annotations.ReadOnlyHint {
		t.Error("gateway with only read-only tools should advertise readOnlyHint=true")
	}

	gw.Register("dangerous", nil, GatewayEntry{Destructive: true})
	tool = gw.Tool()
	if !*tool.Annotations.DestructiveHint {
		t.Error("gateway proxying a destructive tool must advertise destructiveHint=true")
	}
	if tool.Annotations.ReadOnlyHint {
		t.Error("gateway with a write tool must not advertise readOnlyHint=true")
	}
}

// TestServerCapabilities_NoUnbackedSubscribe — we do not install a
// SubscribeHandler, so subscribe must not be advertised.
func TestServerCapabilities_NoUnbackedSubscribe(t *testing.T) {
	opts := &mcpsdk.ServerOptions{}
	NewConfiguredServer(Deps{}, false, opts)
	if opts.Capabilities.Resources.Subscribe {
		t.Error("resources.subscribe advertised without a SubscribeHandler")
	}
}

// ---------------------------------------------------------------------------
// Activity log: redaction and attribution
// ---------------------------------------------------------------------------

type capturingActivityStore struct {
	store.MCPActivityStore
	mu     sync.Mutex
	params []store.LogMCPActivityParams
}

func (c *capturingActivityStore) Log(_ context.Context, p store.LogMCPActivityParams) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.params = append(c.params, p)
	return nil
}

func (c *capturingActivityStore) all() []store.LogMCPActivityParams {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]store.LogMCPActivityParams(nil), c.params...)
}

func runLoggedCall(t *testing.T, ctx context.Context, args map[string]any) store.LogMCPActivityParams {
	t.Helper()
	cap := &capturingActivityStore{}
	al := NewActivityLogger(context.Background(), cap, 10, 1)
	deps := Deps{ActivityLogger: al, Stores: store.Stores{MCPActivityStore: cap}}

	handler := wrapWithActivityLog(deps, "connectors", func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return NewToolResultText(`{"ok":true}`), nil
	})
	if _, err := handler(ctx, MakeCallToolRequest("connectors", args)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	al.Close()

	entries := cap.all()
	if len(entries) != 1 {
		t.Fatalf("logged %d entries, want 1", len(entries))
	}
	return entries[0]
}

// TestActivityLog_RedactsCredentials — a connector DSN must never be persisted.
func TestActivityLog_RedactsCredentials(t *testing.T) {
	entry := runLoggedCall(t, context.Background(), map[string]any{
		"action": "create",
		"params": map[string]any{
			"connection_string": "postgres://app:S3cret@db.prod:5432/app",
			"auth_token":        "tok_live_abcdef",
			"name":              "prod-db",
		},
	})
	if strings.Contains(entry.Arguments, "S3cret") || strings.Contains(entry.Arguments, "tok_live") {
		t.Errorf("credentials persisted to the activity log: %s", entry.Arguments)
	}
	if !strings.Contains(entry.Arguments, redactedPlaceholder) {
		t.Errorf("expected a redaction marker, got: %s", entry.Arguments)
	}
	if !strings.Contains(entry.Arguments, "prod-db") {
		t.Errorf("non-sensitive args should survive: %s", entry.Arguments)
	}
	// Sanity: the preview really is valid JSON with the value swapped.
	var decoded map[string]any
	if err := json.Unmarshal([]byte(entry.Arguments), &decoded); err != nil {
		t.Fatalf("args preview is not valid JSON: %v", err)
	}
}

// TestActivityLog_RecordsUserAndEnvironment — the audit trail must attribute
// the call.
func TestActivityLog_RecordsUserAndEnvironment(t *testing.T) {
	ctx := srvpkg.WithUser(context.Background(), &store.User{ID: "user-123"})
	ctx = envscope.With(ctx, envscope.EnvScope{Allowed: []string{"production"}})

	entry := runLoggedCall(t, ctx, map[string]any{"action": "list"})
	if entry.UserID != "user-123" {
		t.Errorf("UserID = %q, want user-123", entry.UserID)
	}
	if entry.Environment != "production" {
		t.Errorf("Environment = %q, want production", entry.Environment)
	}
	if entry.SessionID == "" {
		t.Error("SessionID should never be empty")
	}
}
