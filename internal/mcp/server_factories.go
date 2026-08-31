package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/internal/metrics"
	srvpkg "github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

// wrapWithMetrics wraps a tool handler to record Prometheus metrics for each call.
func wrapWithMetrics(toolName string, handler ToolHandlerFunc) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		metrics.RecordMCPToolCall(toolName)
		return handler(ctx, request)
	}
}

const (
	// activityPreviewMaxLen bounds the argument/result previews stored per
	// activity row.
	activityPreviewMaxLen = 500
	// redactedPlaceholder replaces the value of a sensitive argument.
	redactedPlaceholder = "[REDACTED]"
	// defaultSessionID labels activity rows for calls that arrived without an
	// MCP session (stdio transport, internal dispatch).
	defaultSessionID = "stdio"
)

// sensitiveArgKeys are argument names whose values must never reach the
// activity log. connectors create/update carry live DSNs and API tokens.
var sensitiveArgKeys = map[string]bool{
	"connection_string": true,
	"auth_token":        true,
	"password":          true,
	"token":             true,
	"secret":            true,
	"api_key":           true,
	"access_key":        true,
	"private_key":       true,
	"credentials":       true,
	"dsn":               true,
}

// redactArgs returns a copy of args with sensitive values replaced. Nested
// maps are walked too, so params={connection_string:...} is covered wherever
// it sits in the argument tree.
func redactArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		switch {
		case sensitiveArgKeys[strings.ToLower(k)]:
			out[k] = redactedPlaceholder
		default:
			if nested, ok := v.(map[string]any); ok {
				out[k] = redactArgs(nested)
				continue
			}
			out[k] = v
		}
	}
	return out
}

// wrapWithActivityLog wraps a tool handler to log its execution to the activity store.
func wrapWithActivityLog(deps Deps, toolName string, handler ToolHandlerFunc) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		result, err := handler(ctx, request)
		elapsed := time.Since(start).Milliseconds()

		// Build a brief preview of args, with credentials stripped — this row
		// is persisted and re-served through the admin audit surfaces.
		argsPreview := ""
		if args := GetArguments(request); len(args) > 0 {
			data, _ := json.Marshal(redactArgs(args))
			argsPreview = truncatePreview(string(data))
		}

		// Build result preview
		isError := err != nil
		resultPreview := ""
		if result != nil && len(result.Content) > 0 {
			if txt, ok := result.Content[0].(*mcp.TextContent); ok {
				resultPreview = truncatePreview(txt.Text)
			}
			isError = isError || result.IsError
		}

		// Attribution: who called, on which session, for which env. All three
		// are available from the authenticated ctx / request and the audit
		// surfaces are useless without them.
		sessionID := defaultSessionID
		if request != nil && request.Session != nil {
			if id := request.Session.ID(); id != "" {
				sessionID = id
			}
		}

		// Log via bounded activity logger to avoid unbounded goroutine growth.
		if deps.ActivityLogger != nil {
			deps.ActivityLogger.Log(store.LogMCPActivityParams{
				SessionID:     sessionID,
				UserID:        activityUserID(ctx),
				Environment:   activityEnvironment(ctx),
				ToolName:      toolName,
				Arguments:     argsPreview,
				ResultPreview: resultPreview,
				IsError:       isError,
				DurationMs:    &elapsed,
				EventType:     "tool_call",
			})
		}

		return result, err
	}
}

// truncatePreview clips a preview string to the persisted maximum.
func truncatePreview(s string) string {
	if len(s) <= activityPreviewMaxLen {
		return s
	}
	return s[:activityPreviewMaxLen]
}

// activityUserID returns the authenticated user's ID, or "" when the call did
// not come through the HTTP auth middleware (stdio serves a single fixed user).
func activityUserID(ctx context.Context) string {
	if u := srvpkg.UserFromContext(ctx); u != nil {
		return u.ID
	}
	return ""
}

// activityEnvironment returns the env scope the call ran under: the single env
// for a pinned token, "*" for a wildcard token, and "" when unscoped.
func activityEnvironment(ctx context.Context) string {
	scope, ok := envscope.FromOK(ctx)
	if !ok {
		return ""
	}
	if env, single := scope.SoleEnv(); single {
		return env
	}
	if scope.AllowsAll() {
		return envscope.WildcardEnv
	}
	return strings.Join(scope.Allowed, ",")
}

// wrapHandler applies activity logging and metrics to a handler.
func wrapHandler(deps Deps, toolName string, handler ToolHandlerFunc) ToolHandlerFunc {
	// Innermost: the handler must see an already-resolved window, and the
	// metrics/activity wrappers should time the real work rather than the
	// deploy lookup.
	handler = wrapWithDeployWindow(deps, handler)
	handler = wrapWithMetrics(toolName, handler)
	if deps.MCPActivityStore != nil {
		handler = wrapWithActivityLog(deps, toolName, handler)
	}
	return handler
}

// ---------------------------------------------------------------------------
// Legacy helpers (still needed for dynamic connector tools)
// ---------------------------------------------------------------------------

// convertTool maps a connector.Tool to an mcp.Tool with the appropriate
// JSON Schema properties derived from the tool's parameter definitions.
func convertTool(t connector.Tool) *mcp.Tool {
	props := make(map[string]SchemaProperty)
	var required []string
	for _, p := range t.Params {
		schemaType := "string"
		switch p.Type {
		case "int":
			schemaType = "number"
		case "bool":
			schemaType = "boolean"
		}
		props[p.Name] = SchemaProperty{Type: schemaType}
		if p.Required {
			required = append(required, p.Name)
		}
	}
	return &mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: ToolSchema(props, required),
	}
}

// bridgeHandler wraps a connector.Tool handler as an MCP ToolHandlerFunc.
func bridgeHandler(t connector.Tool) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := GetArguments(request)
		if args == nil {
			args = make(map[string]any)
		}

		result, err := t.Handler(ctx, args)
		if err != nil {
			return NewToolResultError(err.Error()), nil
		}

		return NewToolResultText(result), nil
	}
}
