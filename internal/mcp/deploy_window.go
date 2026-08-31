package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/mcp/tools"
)

// DeployWindowToken is the time-window value that means "since the most recent
// deploy in scope". It is the question actually being asked most of the time,
// and expressing it as a wall-clock guess ("6h"?) is how a caller ends up
// reading the wrong window's numbers.
const DeployWindowToken = "last_deploy"

// windowArgKeys are the argument names that carry a time window, in the same
// priority order tools.OptionalSinceParam reads them.
var windowArgKeys = []string{"since", "time_range", "timeframe"}

// wrapWithDeployWindow resolves DeployWindowToken into a concrete RFC3339
// timestamp before the tool handler runs.
//
// It lives in the middleware rather than in tools.ParseSince because resolving
// a deploy needs the store and the caller's env scope, and ParseSince is a pure
// string parser used from contexts that have neither. Rewriting the request
// once here means every tool that accepts a window understands the token, with
// no change to the eleven call sites that read it.
func wrapWithDeployWindow(deps Deps, handler ToolHandlerFunc) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := GetArguments(request)

		key := ""
		for _, k := range windowArgKeys {
			if v, ok := args[k].(string); ok && v == DeployWindowToken {
				key = k
				break
			}
		}
		if key == "" {
			return handler(ctx, request)
		}

		if deps.DeployStore == nil {
			return NewToolResultError("deploy tracking is not enabled on this server"), nil
		}

		// Env scope decides which deploy the caller may see: a staging-scoped
		// token asking for "last_deploy" must not get production's.
		env, err := tools.ResolveEnv(ctx, args)
		if err != nil {
			return NewToolResultError(err.Error()), nil
		}

		d, err := deps.DeployStore.Latest(ctx, tools.ArgString(args, "service"), env)
		if err != nil || d == nil {
			return NewToolResultError(fmt.Sprintf(
				"no deploy recorded yet for this scope — %s needs at least one commit hash seen in ingest. "+
					"Use an explicit window like 24h instead.", DeployWindowToken)), nil
		}

		args[key] = d.FirstSeenAt.UTC().Format(time.RFC3339)
		patched, err := json.Marshal(args)
		if err != nil {
			return NewToolResultError("rewriting time window failed"), nil
		}
		// GetArguments re-unmarshals from the raw payload on every call, so the
		// rewrite has to land there rather than in the decoded map.
		rewritten := *request
		params := *request.Params
		params.Arguments = patched
		rewritten.Params = &params

		return handler(ctx, &rewritten)
	}
}
