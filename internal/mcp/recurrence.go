package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// RecurrenceDetector detects and links recurring investigations.
// It tracks which watchers, errors, and health checks are associated
// with each investigation session, then detects when the same issue
// recurs by finding previous sessions that dealt with the same entity.
type RecurrenceDetector struct {
	sessionStore store.InvestigationSessionStore
}

// NewRecurrenceDetector creates a new RecurrenceDetector.
func NewRecurrenceDetector(sessionStore store.InvestigationSessionStore) *RecurrenceDetector {
	return &RecurrenceDetector{sessionStore: sessionStore}
}

// withTimeout creates a context with a 5s timeout for fire-and-forget operations.
func (rd *RecurrenceDetector) withTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 5*time.Second)
}

// getSession fetches the current session, returning nil if not found.
func (rd *RecurrenceDetector) getSession(ctx context.Context, sessionID string) *store.InvestigationSession {
	sess, err := rd.sessionStore.GetByID(ctx, sessionID)
	if err != nil {
		return nil
	}
	return sess
}

// LinkCreatedWatcher appends a watcher ID to the session's created_watcher_ids
// and sets the recurrence group to "watcher:{id}".
func (rd *RecurrenceDetector) LinkCreatedWatcher(ctx context.Context, sessionID, watcherID string) {
	ctx, cancel := rd.withTimeout(ctx)
	defer cancel()

	sess := rd.getSession(ctx, sessionID)
	if sess == nil {
		return
	}

	ids := appendUnique(sess.CreatedWatcherIDs, watcherID)
	group := "watcher:" + watcherID

	err := rd.sessionStore.Update(ctx, sessionID, store.UpdateInvestigationSessionParams{
		CreatedWatcherIDs: ids,
		RecurrenceGroup:   &group,
	})
	if err != nil {
		slog.Debug("recurrence: failed to link created watcher", "error", err, "session_id", sessionID)
	}
}

// LinkTriggeredWatcher sets the triggered_by_watcher_id on the session and
// detects recurrence by finding the previous session that created this watcher.
func (rd *RecurrenceDetector) LinkTriggeredWatcher(ctx context.Context, sessionID, watcherID string) {
	ctx, cancel := rd.withTimeout(ctx)
	defer cancel()

	params := store.UpdateInvestigationSessionParams{
		TriggeredByWatcherID: &watcherID,
	}

	// Find the session that originally created this watcher.
	prev, err := rd.sessionStore.FindByCreatedWatcher(ctx, watcherID)
	if err != nil {
		slog.Debug("recurrence: failed to find previous watcher session", "error", err)
	}
	if prev != nil {
		params.PreviousSessionID = &prev.ID
		count := prev.RecurrenceCount + 1
		params.RecurrenceCount = &count
		group := "watcher:" + watcherID
		params.RecurrenceGroup = &group

		if prev.EndedAt != nil {
			durability := int(time.Since(*prev.EndedAt).Seconds())
			params.FixDurabilitySeconds = &durability
		}
	}

	err = rd.sessionStore.Update(ctx, sessionID, params)
	if err != nil {
		slog.Debug("recurrence: failed to link triggered watcher", "error", err, "session_id", sessionID)
	}
}

// LinkInvestigatedError appends a fingerprint to the session's investigated_error_fingerprints.
func (rd *RecurrenceDetector) LinkInvestigatedError(ctx context.Context, sessionID, fingerprint string) {
	ctx, cancel := rd.withTimeout(ctx)
	defer cancel()

	sess := rd.getSession(ctx, sessionID)
	if sess == nil {
		return
	}

	fps := appendUnique(sess.InvestigatedErrorFingerprints, fingerprint)

	err := rd.sessionStore.Update(ctx, sessionID, store.UpdateInvestigationSessionParams{
		InvestigatedErrorFingerprints: fps,
	})
	if err != nil {
		slog.Debug("recurrence: failed to link investigated error", "error", err, "session_id", sessionID)
	}
}

// LinkResolvedError appends a fingerprint to the session's resolved_error_group_ids
// and sets the recurrence group.
func (rd *RecurrenceDetector) LinkResolvedError(ctx context.Context, sessionID, fingerprint string) {
	ctx, cancel := rd.withTimeout(ctx)
	defer cancel()

	sess := rd.getSession(ctx, sessionID)
	if sess == nil {
		return
	}

	ids := appendUnique(sess.ResolvedErrorGroupIDs, fingerprint)
	group := "error:" + fingerprint

	err := rd.sessionStore.Update(ctx, sessionID, store.UpdateInvestigationSessionParams{
		ResolvedErrorGroupIDs: ids,
		RecurrenceGroup:       &group,
	})
	if err != nil {
		slog.Debug("recurrence: failed to link resolved error", "error", err, "session_id", sessionID)
	}
}

// DetectErrorRecurrence detects if this error was previously resolved and has recurred.
// It links to the previous resolver session and sets the recurrence chain.
func (rd *RecurrenceDetector) DetectErrorRecurrence(ctx context.Context, sessionID, fingerprint string, reopenedCount int) {
	if reopenedCount == 0 {
		return
	}

	ctx, cancel := rd.withTimeout(ctx)
	defer cancel()

	prev, err := rd.sessionStore.FindByResolvedError(ctx, fingerprint)
	if err != nil {
		slog.Debug("recurrence: failed to find previous error resolver", "error", err)
		return
	}
	if prev == nil {
		return
	}

	params := store.UpdateInvestigationSessionParams{
		PreviousSessionID: &prev.ID,
	}
	count := prev.RecurrenceCount + 1
	params.RecurrenceCount = &count
	group := "error:" + fingerprint
	params.RecurrenceGroup = &group

	if prev.EndedAt != nil {
		durability := int(time.Since(*prev.EndedAt).Seconds())
		params.FixDurabilitySeconds = &durability
	}

	err = rd.sessionStore.Update(ctx, sessionID, params)
	if err != nil {
		slog.Debug("recurrence: failed to set error recurrence", "error", err, "session_id", sessionID)
	}
}

// LinkCreatedHealthcheck appends a health check ID to the session's created_healthcheck_ids
// and sets the recurrence group.
func (rd *RecurrenceDetector) LinkCreatedHealthcheck(ctx context.Context, sessionID, healthcheckID string) {
	ctx, cancel := rd.withTimeout(ctx)
	defer cancel()

	sess := rd.getSession(ctx, sessionID)
	if sess == nil {
		return
	}

	ids := appendUnique(sess.CreatedHealthcheckIDs, healthcheckID)
	group := "healthcheck:" + healthcheckID

	err := rd.sessionStore.Update(ctx, sessionID, store.UpdateInvestigationSessionParams{
		CreatedHealthcheckIDs: ids,
		RecurrenceGroup:       &group,
	})
	if err != nil {
		slog.Debug("recurrence: failed to link created healthcheck", "error", err, "session_id", sessionID)
	}
}

// LinkTriggeredHealthcheck sets the triggered_by_healthcheck_id on the session
// and detects recurrence by finding the previous session that created this health check.
func (rd *RecurrenceDetector) LinkTriggeredHealthcheck(ctx context.Context, sessionID, healthcheckID string) {
	ctx, cancel := rd.withTimeout(ctx)
	defer cancel()

	params := store.UpdateInvestigationSessionParams{
		TriggeredByHealthcheckID: &healthcheckID,
	}

	prev, err := rd.sessionStore.FindByCreatedHealthcheck(ctx, healthcheckID)
	if err != nil {
		slog.Debug("recurrence: failed to find previous healthcheck session", "error", err)
	}
	if prev != nil {
		params.PreviousSessionID = &prev.ID
		count := prev.RecurrenceCount + 1
		params.RecurrenceCount = &count
		group := "healthcheck:" + healthcheckID
		params.RecurrenceGroup = &group

		if prev.EndedAt != nil {
			durability := int(time.Since(*prev.EndedAt).Seconds())
			params.FixDurabilitySeconds = &durability
		}
	}

	err = rd.sessionStore.Update(ctx, sessionID, params)
	if err != nil {
		slog.Debug("recurrence: failed to link triggered healthcheck", "error", err, "session_id", sessionID)
	}
}

// BuildEscalationNote generates a deterministic escalation message based on recurrence data.
func BuildEscalationNote(sess *store.InvestigationSession) string {
	if sess == nil || sess.RecurrenceCount == 0 {
		return ""
	}

	note := fmt.Sprintf("Recurrence #%d.", sess.RecurrenceCount)

	if sess.FixDurabilitySeconds != nil {
		dur := *sess.FixDurabilitySeconds
		switch {
		case dur < 3600:
			note += fmt.Sprintf(" Last fix held for %dm.", dur/60)
		case dur < 86400:
			note += fmt.Sprintf(" Last fix held for %dh.", dur/3600)
		default:
			note += fmt.Sprintf(" Last fix held for %dd.", dur/86400)
		}
	}

	if sess.RecurrenceCount >= 3 {
		note += " Fixes not holding, consider root cause analysis."
	}

	return note
}

// InjectRecurrenceContext enriches a tool response map with investigation context
// from a previous session. Call this before JSON marshaling the response.
func (rd *RecurrenceDetector) InjectRecurrenceContext(ctx context.Context, sessionID string, resp map[string]any) {
	if sessionID == "" {
		return
	}

	ctx, cancel := rd.withTimeout(ctx)
	defer cancel()

	sess := rd.getSession(ctx, sessionID)
	if sess == nil || sess.PreviousSessionID == nil {
		return
	}

	prev, err := rd.sessionStore.GetByID(ctx, *sess.PreviousSessionID)
	if err != nil {
		return
	}

	invCtx := map[string]any{
		"recurrence_count":   sess.RecurrenceCount,
		"previous_session":   prev.ID,
		"previous_status":    string(prev.Status),
		"previous_intent":    prev.Intent,
		"previous_steps":     prev.TotalSteps,
	}

	if prev.Summary != "" {
		invCtx["previous_fix_summary"] = prev.Summary
	}
	if prev.RootCause != "" {
		invCtx["previous_root_cause"] = prev.RootCause
	}
	if prev.FixDescription != "" {
		invCtx["previous_fix_description"] = prev.FixDescription
	}
	if sess.FixDurabilitySeconds != nil {
		invCtx["fix_durability_seconds"] = *sess.FixDurabilitySeconds
	}

	escalation := BuildEscalationNote(sess)
	if escalation != "" {
		invCtx["escalation_note"] = escalation
	}

	resp["investigation_context"] = invCtx
}

// appendUnique appends val to slice if not already present.
func appendUnique(slice []string, val string) []string {
	for _, v := range slice {
		if v == val {
			return slice
		}
	}
	return append(slice, val)
}
