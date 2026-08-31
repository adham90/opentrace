package tools

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/pkg/store"
)

// firstCatchupWindow bounds the very first catch-up for a user who has never
// called it. Replaying all of history on day one buries the thing that actually
// happened last night under months of noise.
const firstCatchupWindow = 24 * time.Hour

// maxCatchupWindow bounds a stale cursor. Coming back from a two-week holiday
// should surface the last few days, not two weeks of every alert that ever
// fired — that is a report, not a queue to drain.
const maxCatchupWindow = 7 * 24 * time.Hour

// maxCatchupItems caps the response. Past this the answer stops being
// actionable and starts being a log dump.
const maxCatchupItems = 50

// catchupFetchLimit is what each collector asks its store for: one more than
// the response cap, so a single source overflowing on its own still trips the
// truncation notice. Capping each collector at exactly maxCatchupItems would
// silently return a full page and claim it was everything.
const catchupFetchLimit = maxCatchupItems + 1

// catchupAlertScanLimit is how many recent alerts to pull before filtering by
// cursor. WatchStore.ListAlerts has no time filter, so the window is applied
// here; the limit is generous enough that a busy night is not silently cut.
const catchupAlertScanLimit = 200

// HandleCatchup answers "what happened while I was gone" for the calling user.
//
// The cursor is per user, not global: on a small team, one person draining the
// queue must not hide the same incident from everyone else. It advances only
// after a response is successfully assembled — advancing on entry would let a
// failed call silently swallow a night's events.
func HandleCatchup(ctx context.Context, d OverviewDeps, args map[string]any) (*CallToolResult, error) {
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	scope, _ := envscope.FromOK(ctx)
	userID := scope.UserID

	since, firstRun := catchupSince(ctx, d, userID)
	now := time.Now().UTC()

	var items []triageEntry
	items = append(items, catchupErrorGroups(ctx, d, env, since)...)
	items = append(items, catchupAlerts(ctx, d, env, since)...)
	items = append(items, catchupDeploys(ctx, d, env, since)...)

	sort.Slice(items, func(i, j int) bool {
		si, sj := sevOrder(items[i].Severity), sevOrder(items[j].Severity)
		if si != sj {
			return si < sj
		}
		return items[i].Time > items[j].Time
	})

	truncated := false
	if len(items) > maxCatchupItems {
		items = items[:maxCatchupItems]
		truncated = true
	}

	// Advance last, and only when asked to. A caller re-reading after a context
	// compaction needs to see the same window again, not an empty one.
	advanced := false
	if !ArgBool(args, "peek") && userID != "" && d.UserStore != nil {
		if err := d.UserStore.SetCatchupCursor(ctx, userID, now); err != nil {
			// A cursor that failed to advance means the next call repeats this
			// window — noisy, but strictly better than losing events.
			return JSONResult(catchupResponse(items, since, now, firstRun, truncated, false))
		}
		advanced = true
	}

	resp := catchupResponse(items, since, now, firstRun, truncated, advanced)
	if len(items) == 0 {
		return JSONResult(resp)
	}
	return JSONResult(resp, catchupSuggestions(items)...)
}

func catchupResponse(items []triageEntry, since, now time.Time, firstRun, truncated, advanced bool) map[string]any {
	resp := map[string]any{
		"since":           since.Format(time.RFC3339),
		"until":           now.Format(time.RFC3339),
		"count":           len(items),
		"items":           items,
		"cursor_advanced": advanced,
	}
	if firstRun {
		resp["note"] = fmt.Sprintf(
			"first catch-up for this token — showing the last %s rather than all history", firstCatchupWindow)
	}
	if truncated {
		resp["truncated"] = fmt.Sprintf("more than %d events; showing the most severe and most recent", maxCatchupItems)
	}
	if len(items) == 0 {
		resp["summary"] = "Nothing new since your last catch-up."
	}
	return resp
}

// catchupSince resolves the window start, clamped at both ends. Returns whether
// this is the caller's first catch-up so the response can say so.
func catchupSince(ctx context.Context, d OverviewDeps, userID string) (time.Time, bool) {
	now := time.Now().UTC()
	if userID == "" || d.UserStore == nil {
		// No identity (stdio without a user, or a test harness): fall back to a
		// fixed window rather than failing. It still answers the question.
		return now.Add(-firstCatchupWindow), true
	}

	cursor, err := d.UserStore.CatchupCursor(ctx, userID)
	if err != nil || cursor.IsZero() {
		return now.Add(-firstCatchupWindow), true
	}
	if oldest := now.Add(-maxCatchupWindow); cursor.Before(oldest) {
		return oldest, false
	}
	return cursor.UTC(), false
}

// catchupErrorGroups returns error groups first seen since the cursor. Groups
// that merely recurred are excluded on purpose: an ongoing error is triage's
// job, and repeating it every morning is what makes a queue get ignored.
func catchupErrorGroups(ctx context.Context, d OverviewDeps, env string, since time.Time) []triageEntry {
	if d.ErrorGroupStore == nil {
		return nil
	}
	groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
		Status:      store.ErrorGroupUnresolved,
		Environment: env,
		Since:       &since,
		SortBy:      "occurrence_count",
		Limit:       catchupFetchLimit,
	})
	if err != nil {
		return nil
	}

	items := make([]triageEntry, 0, len(groups))
	for _, eg := range groups {
		msg := eg.Message
		if len(msg) > 80 {
			msg = msg[:80] + "..."
		}
		title := msg
		if eg.ExceptionClass != "" {
			title = eg.ExceptionClass + ": " + msg
		}
		detail := fmt.Sprintf("new error — %d occurrences since first seen", eg.OccurrenceCount)
		if env == "" && eg.Environment != "" {
			detail += " [" + eg.Environment + "]"
		}
		items = append(items, triageEntry{
			Type:        "error_group",
			Severity:    "critical",
			Title:       title,
			Detail:      detail,
			Time:        eg.FirstSeenAt.Format(time.RFC3339),
			ID:          eg.Fingerprint,
			Environment: eg.Environment,
		})
	}
	return items
}

func catchupAlerts(ctx context.Context, d OverviewDeps, env string, since time.Time) []triageEntry {
	if d.WatchStore == nil {
		return nil
	}
	// Empty status: an alert that fired and was acknowledged overnight still
	// happened while the caller was away, and hiding it is how a team member
	// finds out about an incident from a customer instead.
	alerts, err := d.WatchStore.ListAlerts(ctx, "", "", catchupAlertScanLimit)
	if err != nil {
		return nil
	}

	var items []triageEntry
	for _, a := range alerts {
		if !a.CreatedAt.After(since) {
			continue
		}
		if env != "" && a.Environment != env {
			continue
		}
		severity := "warning"
		if a.Status == "pending" {
			severity = "critical"
		}
		items = append(items, triageEntry{
			Type:        "watch_alert",
			Severity:    severity,
			Title:       a.Summary,
			Detail:      fmt.Sprintf("%s: %.2f (threshold %.2f) — status %s", a.TriggerMetric(), a.TriggerValue(), a.ThresholdValue(), a.Status),
			Time:        a.CreatedAt.Format(time.RFC3339),
			ID:          a.ID,
			Environment: a.Environment,
		})
	}
	return items
}

func catchupDeploys(ctx context.Context, d OverviewDeps, env string, since time.Time) []triageEntry {
	if d.DeployStore == nil {
		return nil
	}
	deploys, err := d.DeployStore.List(ctx, store.ListDeployParams{
		Environment: env,
		Since:       &since,
		Limit:       catchupFetchLimit,
	})
	if err != nil {
		return nil
	}

	items := make([]triageEntry, 0, len(deploys))
	for _, dep := range deploys {
		short := dep.CommitHash
		if len(short) > 7 {
			short = short[:7]
		}
		detail := "deployed"
		if dep.Service != "" {
			detail = "deployed to " + dep.Service
		}
		items = append(items, triageEntry{
			Type:        "deploy",
			Severity:    "info",
			Title:       "Deploy " + short,
			Detail:      detail,
			Time:        dep.FirstSeenAt.Format(time.RFC3339),
			ID:          dep.CommitHash,
			Environment: dep.Environment,
		})
	}
	return items
}

// catchupSuggestions points at the top item and, when a deploy is in the
// window, at the comparison that explains the rest of it.
func catchupSuggestions(items []triageEntry) []ToolSuggestion {
	var suggestions []ToolSuggestion
	top := items[0]

	switch top.Type {
	case "error_group":
		detailArgs := map[string]any{"action": "detail", "fingerprint": top.ID}
		if top.Environment != "" {
			detailArgs["environment"] = top.Environment
		}
		suggestions = append(suggestions, Suggest("errors", "Investigate the newest error", detailArgs))
	case "watch_alert":
		suggestions = append(suggestions, Suggest("watches", "Investigate the alert", map[string]any{
			"action": "investigate", "alert_id": top.ID,
		}))
	}

	for _, it := range items {
		if it.Type == "deploy" {
			suggestions = append(suggestions, Suggest("logs", "Compare against the deploy in this window", map[string]any{
				"action": "compare", "since": "last_deploy",
			}))
			break
		}
	}
	return suggestions
}
