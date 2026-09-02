package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// SessionSummaryCallback is a callback for handling session summary updates.
// Defined here so overview (read-only) can call it without importing admin.
type SessionSummaryCallback func(ctx context.Context, args map[string]any) (*CallToolResult, error)

// OverviewDeps holds the stores needed by the overview tool.
type OverviewDeps struct {
	LogStore         store.LogStore
	DSStore          store.DataSourceStore
	ServerStore      store.ServerStore
	ErrorGroupStore  store.ErrorGroupStore
	WatchStore       store.WatchStore
	HealthCheckStore store.HealthCheckStore
	SettingsStore    store.SettingsStore
	AgentNoteStore   store.AgentNoteStore
	DeployStore      store.DeployStore
	UserStore        store.UserStore
	SessionSummary   SessionSummaryCallback

	// CriticalPaths are the money-path patterns from config. Empty means every
	// item ranks on severity alone.
	CriticalPaths []string

	// OnCallStatus reports the on-call agent's health. A function rather than
	// the runner itself: the tools package has no business importing the agent,
	// and this is the only thing it needs from it. Nil when not configured.
	OnCallStatus func() (lastSuccess time.Time, lastError string, runsToday int)
}

// OverviewHandler returns a handler for the consolidated overview tool.
func OverviewHandler(d OverviewDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action := ArgString(args, "action")

		switch action {
		case "status":
			return HandleOverviewStatus(ctx, d, args)
		case "triage":
			return HandleTriage(ctx, d, args)
		case "catchup":
			return HandleCatchup(ctx, d, args)
		case "diagnose":
			return HandleDiagnose(ctx, d, args)
		case "timeline":
			return HandleTimeline(ctx, d, args)
		case "investigate":
			return HandleOverviewInvestigate(ctx, d, args)
		case "changes":
			return HandleChanges(ctx, d, args)
		case "settings":
			return HandleOverviewSettings(ctx, d)
		case "notes":
			return HandleOverviewNotes(ctx, d, args)
		case "delete_note":
			return HandleOverviewDeleteNote(ctx, d, args)
		case "session_summary":
			if d.SessionSummary == nil {
				return NewToolResultError("session tracking is not enabled"), nil
			}
			return d.SessionSummary(ctx, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use status, triage, catchup, diagnose, timeline, investigate, changes, settings, notes, delete_note, session_summary)", action)), nil
		}
	}
}
