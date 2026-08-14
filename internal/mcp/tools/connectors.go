package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/pkg/store"
)

// ConnectorsDeps holds the stores needed by the connectors tool.
type ConnectorsDeps struct {
	DSStore       store.DataSourceStore
	Registry      *connector.Registry
	LogStore      store.LogStore
	Config        *config.Config
	SettingsStore store.SettingsStore
	// IsAdmin reports whether the MCP server this tool is registered on serves
	// an admin token. The connectors tool is mounted on the member (read-only)
	// server too, so every action that mutates state or dials out with stored
	// credentials — create, update, delete, test — requires it.
	IsAdmin bool
}

// requireConnectorAdmin returns an error result when the caller is not an
// admin. Write actions fail closed.
func requireConnectorAdmin(d ConnectorsDeps, action string) *CallToolResult {
	if d.IsAdmin {
		return nil
	}
	return NewToolResultError(fmt.Sprintf(
		"admin privileges are required to %s connectors", action))
}

// connectorView is the allowlisted, secret-free projection of a
// store.DataSource. DataSource.Config holds raw credentials (the database
// connection string, the Turso auth token, ...) so it is NEVER serialized:
// only the fields named here ever reach an MCP client. Adding a new
// secret-bearing config key therefore cannot leak by default.
type connectorView struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Status        string   `json:"status"`
	StatusMessage string   `json:"status_message,omitempty"`
	Environment   string   `json:"environment,omitempty"`
	Endpoint      string   `json:"endpoint,omitempty"`
	ConfigKeys    []string `json:"config_keys,omitempty"`
	LastTestedAt  string   `json:"last_tested_at,omitempty"`
	CreatedAt     string   `json:"created_at,omitempty"`
	UpdatedAt     string   `json:"updated_at,omitempty"`
	ActiveTools   []string `json:"active_tools,omitempty"`
}

// newConnectorView builds the redacted view of a connector. Endpoint is the
// credential-free label derived from the connection string (scheme, host and
// database only); ConfigKeys reports which config keys are set without
// revealing any value.
func newConnectorView(ds *store.DataSource) connectorView {
	v := connectorView{
		ID:          ds.ID.String(),
		Name:        ds.Name,
		Type:        string(ds.Type),
		Status:      string(ds.Status),
		Environment: ds.Environment,
	}
	if ds.StatusMessage != nil {
		v.StatusMessage = connector.RedactSecrets(*ds.StatusMessage)
	}
	if ds.LastTestedAt != nil {
		v.LastTestedAt = ds.LastTestedAt.Format(time.RFC3339)
	}
	if !ds.CreatedAt.IsZero() {
		v.CreatedAt = ds.CreatedAt.Format(time.RFC3339)
	}
	if !ds.UpdatedAt.IsZero() {
		v.UpdatedAt = ds.UpdatedAt.Format(time.RFC3339)
	}

	keys := make([]string, 0, len(ds.Config))
	for k := range ds.Config {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	v.ConfigKeys = keys

	if raw, ok := ds.Config["connection_string"].(string); ok && raw != "" {
		v.Endpoint = connector.ConnectorLabel(raw)
	}
	return v
}

// connectorErrorText renders an error for the caller with any credential that
// leaked into the driver's message stripped out. Driver errors routinely echo
// the whole DSN back, password included.
func connectorErrorText(format string, err error) string {
	return connector.RedactSecrets(fmt.Sprintf(format, err))
}

// ConnectorsHandler returns a handler for the consolidated connectors tool.
func ConnectorsHandler(d ConnectorsDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action := ArgString(args, "action")

		switch action {
		case "list":
			return HandleConnectorList(ctx, d, args)
		case "get":
			return HandleConnectorGet(ctx, d, args)
		case "create":
			return HandleConnectorCreate(ctx, d, args)
		case "test":
			return HandleConnectorTest(ctx, d, args)
		case "update":
			return HandleConnectorUpdate(ctx, d, args)
		case "delete":
			return HandleConnectorDelete(ctx, d, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use list, get, create, test, update, delete)", action)), nil
		}
	}
}

// HandleConnectorList returns the redacted view of every connector. It never
// includes Config values — see connectorView.
func HandleConnectorList(ctx context.Context, d ConnectorsDeps, args map[string]any) (*CallToolResult, error) {
	if d.DSStore != nil {
		var params store.ListDataSourceParams
		if v := ArgString(args, "type"); v != "" {
			params.Type = store.ConnectorType(v)
		}

		connectors, err := d.DSStore.List(ctx, params)
		if err != nil {
			return NewToolResultError(connectorErrorText("failed to list connectors: %v", err)), nil
		}
		if len(connectors) == 0 {
			return EmptyResult("No connectors found.")
		}

		activeToolNames := make([]string, 0)
		if d.Registry != nil {
			for _, t := range d.Registry.AllTools() {
				activeToolNames = append(activeToolNames, t.Name)
			}
		}

		entries := make([]connectorView, 0, len(connectors))
		for i := range connectors {
			c := &connectors[i]
			e := newConnectorView(c)
			if c.Status == store.StatusConnected {
				e.ActiveTools = activeToolNames
			}
			entries = append(entries, e)
		}

		return JSONResult(entries)
	}

	// Fallback: no store, just list active registry tools.
	if d.Registry == nil {
		return EmptyResult("No connectors are currently active.")
	}
	tools := d.Registry.AllTools()
	if len(tools) == 0 {
		return EmptyResult("No connectors are currently active.")
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Active tools (%d):\n", len(tools)))
	for _, t := range tools {
		b.WriteString(fmt.Sprintf("- %s: %s\n", t.Name, t.Description))
	}

	return NewToolResultText(b.String()), nil
}

func HandleConnectorGet(ctx context.Context, d ConnectorsDeps, args map[string]any) (*CallToolResult, error) {
	if d.DSStore == nil {
		return NewToolResultError("DataSourceStore not configured"), nil
	}

	idStr := ArgString(args, "connector_id")
	if idStr == "" {
		return NewToolResultError("connector_id is required"), nil
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		return NewToolResultError("invalid connector_id format"), nil
	}

	ds, err := d.DSStore.GetByID(ctx, id)
	if err != nil {
		if err == store.ErrNotFound {
			return NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
		}
		return NewToolResultError(connectorErrorText("failed to fetch connector: %v", err)), nil
	}

	// Never return ds directly: its Config map holds the plaintext connection
	// string / auth token.
	return JSONResult(newConnectorView(ds))
}

func HandleConnectorCreate(ctx context.Context, d ConnectorsDeps, args map[string]any) (*CallToolResult, error) {
	if errResult := requireConnectorAdmin(d, "create"); errResult != nil {
		return errResult, nil
	}
	if d.DSStore == nil {
		return NewToolResultError("DataSourceStore not configured"), nil
	}

	name := ArgString(args, "name")
	if name == "" {
		return NewToolResultError("name is required"), nil
	}

	dsType := ArgString(args, "connector_type")
	if dsType == "" {
		return NewToolResultError("connector_type is required (database, mysql, redis, turso, or logs)"), nil
	}
	validTypes := map[string]bool{
		"database": true, "mysql": true, "redis": true, "turso": true, "logs": true,
	}
	if !validTypes[dsType] {
		return NewToolResultError("connector_type must be one of: database, mysql, redis, turso, logs"), nil
	}

	needsConnStr := map[string]bool{
		"database": true, "mysql": true, "redis": true, "turso": true,
	}

	cfg := make(map[string]any)
	if connStr := ArgString(args, "connection_string"); connStr != "" {
		cfg["connection_string"] = connStr
	} else if needsConnStr[dsType] {
		return NewToolResultError(fmt.Sprintf("connection_string is required for %s connectors", dsType)), nil
	}

	if dsType == "turso" {
		if authToken := ArgString(args, "auth_token"); authToken != "" {
			cfg["auth_token"] = authToken
		}
	}

	// Connectors support both env-scoped and wildcard ("*") creation, so we
	// take the arg as-is (subject to scope) rather than forcing auto-fill.
	// An explicit environment="*" marks the connector as shared across envs.
	envArg := ArgString(args, "environment")
	if envArg != envscope.WildcardEnv {
		resolved, err := ResolveEnv(ctx, args)
		if err != nil {
			return NewToolResultError(err.Error()), nil
		}
		envArg = resolved
	}

	ds, err := d.DSStore.Create(ctx, store.CreateDataSourceParams{
		Type:        store.ConnectorType(dsType),
		Name:        name,
		Config:      cfg,
		Environment: envArg,
	})
	if err != nil {
		return NewToolResultError(connectorErrorText("failed to create connector: %v", err)), nil
	}

	return NewToolResultText(fmt.Sprintf("Connector %q created (id: %s, type: %s, status: %s). Use connectors with action=test to verify the connection.", ds.Name, ds.ID, ds.Type, ds.Status)), nil
}

func HandleConnectorTest(ctx context.Context, d ConnectorsDeps, args map[string]any) (*CallToolResult, error) {
	// test dials the target with the stored credentials and mutates the
	// connector's status (and the live registry) — admin only.
	if errResult := requireConnectorAdmin(d, "test"); errResult != nil {
		return errResult, nil
	}
	if d.DSStore == nil {
		return NewToolResultError("DataSourceStore not configured"), nil
	}

	idStr := ArgString(args, "connector_id")
	if idStr == "" {
		return NewToolResultError("connector_id is required"), nil
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		return NewToolResultError("invalid connector_id format"), nil
	}

	ds, err := d.DSStore.GetByID(ctx, id)
	if err != nil {
		if err == store.ErrNotFound {
			return NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
		}
		return NewToolResultError(connectorErrorText("failed to fetch connector: %v", err)), nil
	}

	// Driver errors echo the DSN (password included) — redact before the message
	// is stored on the connector or returned to the caller.
	c, err := connector.CreateConnector(ctx, *ds, d.LogStore, d.Config, d.SettingsStore)
	if err != nil {
		status := store.StatusError
		msg := connectorErrorText("failed to create connector: %v", err)
		d.DSStore.Update(ctx, id, store.UpdateDataSourceParams{
			Status: &status, StatusMessage: &msg,
		})
		return NewToolResultError(msg), nil
	}

	if err := c.TestConnection(ctx); err != nil {
		c.Close()
		status := store.StatusError
		msg := connectorErrorText("connection test failed: %v", err)
		now := time.Now()
		d.DSStore.Update(ctx, id, store.UpdateDataSourceParams{
			Status: &status, StatusMessage: &msg, LastTestedAt: &now,
		})
		return NewToolResultError(msg), nil
	}

	if d.Registry != nil {
		d.Registry.Register(c)
	}

	status := store.StatusConnected
	now := time.Now()
	d.DSStore.Update(ctx, id, store.UpdateDataSourceParams{
		Status: &status, LastTestedAt: &now,
	})

	return NewToolResultText(fmt.Sprintf("Connector %q (%s) tested and connected successfully.", ds.Name, ds.Type)), nil
}

func HandleConnectorUpdate(ctx context.Context, d ConnectorsDeps, args map[string]any) (*CallToolResult, error) {
	if errResult := requireConnectorAdmin(d, "update"); errResult != nil {
		return errResult, nil
	}
	if d.DSStore == nil {
		return NewToolResultError("DataSourceStore not configured"), nil
	}

	idStr := ArgString(args, "connector_id")
	if idStr == "" {
		return NewToolResultError("connector_id is required"), nil
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		return NewToolResultError("invalid connector_id format"), nil
	}

	ds, err := d.DSStore.GetByID(ctx, id)
	if err != nil {
		if err == store.ErrNotFound {
			return NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
		}
		return NewToolResultError(connectorErrorText("failed to fetch connector: %v", err)), nil
	}

	var params store.UpdateDataSourceParams

	if v := ArgString(args, "name"); v != "" {
		params.Name = &v
	}

	configChanged := false
	if connStr := ArgString(args, "connection_string"); connStr != "" {
		params.Config = map[string]any{"connection_string": connStr}
		configChanged = true
	}

	if configChanged && d.Registry != nil {
		d.Registry.Unregister(connector.ConnectorType(string(ds.Type)))
	}

	updated, err := d.DSStore.Update(ctx, id, params)
	if err != nil {
		if err == store.ErrNotFound {
			return NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
		}
		return NewToolResultError(connectorErrorText("failed to update connector: %v", err)), nil
	}

	msg := fmt.Sprintf("Connector %q updated successfully.", updated.Name)
	if configChanged {
		msg += " Connection config changed — use connectors with action=test to re-establish the connection."
	}

	return NewToolResultText(msg), nil
}

func HandleConnectorDelete(ctx context.Context, d ConnectorsDeps, args map[string]any) (*CallToolResult, error) {
	if errResult := requireConnectorAdmin(d, "delete"); errResult != nil {
		return errResult, nil
	}
	if d.DSStore == nil {
		return NewToolResultError("DataSourceStore not configured"), nil
	}

	idStr := ArgString(args, "connector_id")
	if idStr == "" {
		return NewToolResultError("connector_id is required"), nil
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		return NewToolResultError("invalid connector_id format"), nil
	}

	ds, err := d.DSStore.GetByID(ctx, id)
	if err != nil {
		if err == store.ErrNotFound {
			return NewToolResultError(fmt.Sprintf("connector %s not found", idStr)), nil
		}
		return NewToolResultError(connectorErrorText("failed to fetch connector: %v", err)), nil
	}

	if d.Registry != nil {
		d.Registry.Unregister(connector.ConnectorType(string(ds.Type)))
	}

	if err := d.DSStore.Delete(ctx, id); err != nil {
		return NewToolResultError(connectorErrorText("failed to delete connector: %v", err)), nil
	}

	return NewToolResultText(fmt.Sprintf("Connector %q (%s) deleted and disconnected.", ds.Name, ds.Type)), nil
}
