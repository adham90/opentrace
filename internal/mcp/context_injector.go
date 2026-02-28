package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// ContextInjector enriches tool responses with investigation memory.
type ContextInjector struct {
	sessionStore     store.InvestigationSessionStore
	transitionStore  store.ToolTransitionStore
	noteStore        store.AgentNoteStore
	runbookStore     store.RunbookEffectivenessStore
	queryMemoryStore store.QueryMemoryStore
	analyticsStore   store.AnalyticsStore
	auditStore       store.AuditStore
	trendStore       store.TrendStore
}

// ContextInjectorDeps holds optional stores for the context injector.
type ContextInjectorDeps struct {
	NoteStore        store.AgentNoteStore
	RunbookStore     store.RunbookEffectivenessStore
	QueryMemoryStore store.QueryMemoryStore
	AnalyticsStore   store.AnalyticsStore
	AuditStore       store.AuditStore
	TrendStore       store.TrendStore
}

// NewContextInjector creates a new ContextInjector.
func NewContextInjector(ss store.InvestigationSessionStore, ts store.ToolTransitionStore, extra ...ContextInjectorDeps) *ContextInjector {
	ci := &ContextInjector{sessionStore: ss, transitionStore: ts}
	if len(extra) > 0 {
		ci.noteStore = extra[0].NoteStore
		ci.runbookStore = extra[0].RunbookStore
		ci.queryMemoryStore = extra[0].QueryMemoryStore
		ci.analyticsStore = extra[0].AnalyticsStore
		ci.auditStore = extra[0].AuditStore
		ci.trendStore = extra[0].TrendStore
	}
	return ci
}

// InvestigationContext holds contextual investigation memory.
type InvestigationContext struct {
	SimilarPastSessions    []SessionSummary           `json:"similar_past_sessions,omitempty"`
	DeadEnds               []DeadEndWarning           `json:"dead_ends,omitempty"`
	ParallelInvestigations []ParallelInfo             `json:"parallel_investigations,omitempty"`
	RelevantNotes          []NoteContext              `json:"relevant_notes,omitempty"`
	RunbookSuggestion      *RunbookSuggestionContext  `json:"runbook_suggestion,omitempty"`
	QueryMemory            []QueryMemoryContext       `json:"query_memory,omitempty"`
	DeployCorrelation      *DeployCorrelationContext  `json:"deploy_correlation,omitempty"`
	TrafficContext         *TrafficContext             `json:"traffic_context,omitempty"`
	RecentAdminActions     []AdminActionContext        `json:"recent_admin_actions,omitempty"`
}

// SessionSummary is a compact representation of a past session.
type SessionSummary struct {
	SessionID   string `json:"session_id"`
	Intent      string `json:"intent"`
	Status      string `json:"status"`
	Summary     string `json:"summary,omitempty"`
	RootCause   string `json:"root_cause,omitempty"`
	TotalSteps  int    `json:"total_steps"`
	Service     string `json:"service,omitempty"`
}

// ParallelInfo describes another active investigation on the same service.
type ParallelInfo struct {
	SessionID string `json:"session_id"`
	UserEmail string `json:"user_email,omitempty"`
	Intent    string `json:"intent"`
	Steps     int    `json:"steps"`
}

// NoteContext is a relevant agent note for the current investigation.
type NoteContext struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Note       string `json:"note"`
}

// RunbookSuggestionContext suggests a runbook based on past effectiveness.
type RunbookSuggestionContext struct {
	RunbookName    string  `json:"runbook_name"`
	ResolutionRate float64 `json:"resolution_rate"`
	Executions     int     `json:"executions"`
}

// QueryMemoryContext holds past investigation context for a query fingerprint.
type QueryMemoryContext struct {
	Fingerprint        string `json:"fingerprint"`
	InvestigationCount int    `json:"investigation_count"`
	LastRootCause      string `json:"last_root_cause,omitempty"`
	LastFix            string `json:"last_fix,omitempty"`
}

// DeployCorrelationContext holds deploy info correlated with the investigation.
type DeployCorrelationContext struct {
	Service     string `json:"service"`
	CommitHash  string `json:"commit_hash"`
	Environment string `json:"environment,omitempty"`
	DeployedAt  string `json:"deployed_at"`
}

// TrafficContext holds traffic anomaly information.
type TrafficContext struct {
	TotalRequests int     `json:"total_requests"`
	ErrorRate     float64 `json:"error_rate"`
	Anomaly       string  `json:"anomaly,omitempty"`
}

// AdminActionContext describes a recent admin action.
type AdminActionContext struct {
	Action     string `json:"action"`
	TargetType string `json:"target_type"`
	UserEmail  string `json:"user_email"`
	CreatedAt  string `json:"created_at"`
}

// BuildContext constructs investigation context for the current session.
// Returns nil for non-investigation intents or when no context is available.
func (ci *ContextInjector) BuildContext(ctx context.Context, sess *store.InvestigationSession, toolName string) *InvestigationContext {
	if sess == nil || sess.Intent != IntentInvestigation {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ic := &InvestigationContext{}

	// 1. Similar past sessions
	similar, err := ci.sessionStore.FindSimilar(ctx, store.FindSimilarParams{
		Service:          sess.PrimaryService,
		Intent:           sess.Intent,
		ToolFingerprint:  sess.ToolFingerprint,
		ExcludeSessionID: sess.ID,
		MaxResults:       3,
		MinSteps:         3,
		OnlyResolved:     true,
	})
	if err != nil {
		slog.Debug("context_injector: FindSimilar failed", "error", err)
	} else {
		for _, s := range similar {
			summary := SessionSummary{
				SessionID:  s.ID,
				Intent:     s.Intent,
				Status:     string(s.Status),
				Summary:    truncate(s.Summary, 200),
				RootCause:  truncate(s.RootCause, 200),
				TotalSteps: s.TotalSteps,
				Service:    s.PrimaryService,
			}
			ic.SimilarPastSessions = append(ic.SimilarPastSessions, summary)
		}
	}

	// 2. Dead-ends from transition data
	if ci.transitionStore != nil {
		transitions, err := ci.transitionStore.GetDeadEnds(ctx, sess.Intent)
		if err != nil {
			slog.Debug("context_injector: dead-end query failed", "error", err)
		} else if len(transitions) > 0 {
			if len(transitions) > 3 {
				transitions = transitions[:3]
			}
			for _, t := range transitions {
				ic.DeadEnds = append(ic.DeadEnds, DeadEndWarning{
					FromTool: t.FromTool,
					ToTool:   t.ToTool,
					Reason:   fmt.Sprintf("This transition was abandoned %d/%d times", t.AbandonedCount, t.TotalCount),
				})
			}
		}
	}

	// 3. Parallel investigations (same service, currently open, different session)
	if sess.PrimaryService != "" {
		recent, err := ci.sessionStore.List(ctx, store.ListInvestigationSessionParams{
			Status:  store.InvestigationStatusOpen,
			Service: sess.PrimaryService,
			Limit:   5,
		})
		if err != nil {
			slog.Debug("context_injector: parallel investigation query failed", "error", err)
		} else {
			for _, s := range recent {
				if s.ID == sess.ID {
					continue
				}
				ic.ParallelInvestigations = append(ic.ParallelInvestigations, ParallelInfo{
					SessionID: s.ID,
					UserEmail: s.UserEmail,
					Intent:    s.Intent,
					Steps:     s.TotalSteps,
				})
			}
		}
	}

	// 4. Relevant notes
	ci.getRelevantNotes(ctx, sess, ic)

	// 5. Runbook suggestion (only at step 0)
	if sess.TotalSteps == 0 {
		ci.suggestRunbook(ctx, ic)
	}

	// 6. Query memory
	ci.getQueryMemory(ctx, sess, ic)

	// 7. Deploy correlation
	ci.getDeployCorrelation(ctx, sess, ic)

	// 8. Traffic context
	ci.getTrafficContext(ctx, sess, ic)

	// 9. Recent admin actions
	ci.getRecentAdminActions(ctx, sess, ic)

	// Return nil if nothing useful was found
	if ci.isContextEmpty(ic) {
		return nil
	}

	// Cap total JSON size to ~4KB with priority truncation
	ci.enforceSize(ic, 4096)

	return ic
}

func (ci *ContextInjector) isContextEmpty(ic *InvestigationContext) bool {
	return len(ic.SimilarPastSessions) == 0 &&
		len(ic.DeadEnds) == 0 &&
		len(ic.ParallelInvestigations) == 0 &&
		len(ic.RelevantNotes) == 0 &&
		ic.RunbookSuggestion == nil &&
		len(ic.QueryMemory) == 0 &&
		ic.DeployCorrelation == nil &&
		ic.TrafficContext == nil &&
		len(ic.RecentAdminActions) == 0
}

// enforceSize truncates context to fit within maxBytes.
// Truncation priority: RelevantNotes first, then RecentAdminActions, then SimilarPastSessions.
func (ci *ContextInjector) enforceSize(ic *InvestigationContext, maxBytes int) {
	data, err := json.Marshal(ic)
	if err != nil || len(data) <= maxBytes {
		return
	}

	// Priority 1: remove RelevantNotes
	for len(ic.RelevantNotes) > 0 && len(data) > maxBytes {
		ic.RelevantNotes = ic.RelevantNotes[:len(ic.RelevantNotes)-1]
		data, _ = json.Marshal(ic)
	}

	// Priority 2: remove RecentAdminActions
	for len(ic.RecentAdminActions) > 0 && len(data) > maxBytes {
		ic.RecentAdminActions = ic.RecentAdminActions[:len(ic.RecentAdminActions)-1]
		data, _ = json.Marshal(ic)
	}

	// Priority 3: remove SimilarPastSessions
	for len(ic.SimilarPastSessions) > 0 && len(data) > maxBytes {
		ic.SimilarPastSessions = ic.SimilarPastSessions[:len(ic.SimilarPastSessions)-1]
		data, _ = json.Marshal(ic)
	}
}

func (ci *ContextInjector) getRelevantNotes(ctx context.Context, sess *store.InvestigationSession, ic *InvestigationContext) {
	if ci.noteStore == nil || sess.PrimaryService == "" {
		return
	}

	notes, err := ci.noteStore.List(ctx, "service")
	if err != nil {
		slog.Debug("context_injector: note query failed", "error", err)
		return
	}

	for _, n := range notes {
		if n.EntityID == sess.PrimaryService {
			ic.RelevantNotes = append(ic.RelevantNotes, NoteContext{
				EntityType: n.EntityType,
				EntityID:   n.EntityID,
				Note:       truncate(n.Note, 300),
			})
		}
	}

	// Also check notes for investigated error fingerprints
	for _, fp := range sess.InvestigatedErrorFingerprints {
		note, err := ci.noteStore.Get(ctx, "error", fp)
		if err != nil || note == nil {
			continue
		}
		ic.RelevantNotes = append(ic.RelevantNotes, NoteContext{
			EntityType: note.EntityType,
			EntityID:   note.EntityID,
			Note:       truncate(note.Note, 300),
		})
	}

	// Cap at 5 notes
	if len(ic.RelevantNotes) > 5 {
		ic.RelevantNotes = ic.RelevantNotes[:5]
	}
}

func (ci *ContextInjector) suggestRunbook(ctx context.Context, ic *InvestigationContext) {
	if ci.runbookStore == nil {
		return
	}

	best, err := ci.runbookStore.GetMostEffective(ctx)
	if err != nil {
		slog.Debug("context_injector: runbook query failed", "error", err)
		return
	}
	if best == nil || best.TotalExecutions == 0 {
		return
	}

	rate := float64(best.ResolvedSessions) / float64(best.TotalExecutions)
	if rate > 0.5 {
		ic.RunbookSuggestion = &RunbookSuggestionContext{
			RunbookName:    best.RunbookName,
			ResolutionRate: rate,
			Executions:     best.TotalExecutions,
		}
	}
}

func (ci *ContextInjector) getQueryMemory(ctx context.Context, sess *store.InvestigationSession, ic *InvestigationContext) {
	if ci.queryMemoryStore == nil {
		return
	}

	for _, fp := range sess.ExplainedQueries {
		qm, err := ci.queryMemoryStore.Get(ctx, fp)
		if err != nil {
			continue
		}
		ic.QueryMemory = append(ic.QueryMemory, QueryMemoryContext{
			Fingerprint:        qm.Fingerprint,
			InvestigationCount: qm.InvestigationCount,
			LastRootCause:      truncate(qm.LastRootCause, 200),
			LastFix:            truncate(qm.LastFix, 200),
		})
	}
}

func (ci *ContextInjector) getDeployCorrelation(ctx context.Context, sess *store.InvestigationSession, ic *InvestigationContext) {
	if ci.trendStore == nil || sess.CorrelatedDeploy == "" {
		return
	}

	markers, err := ci.trendStore.ListDeployMarkers(ctx, sess.PrimaryService, sess.StartedAt.Add(-1*time.Hour))
	if err != nil {
		slog.Debug("context_injector: deploy query failed", "error", err)
		return
	}

	for _, m := range markers {
		if m.CommitHash == sess.CorrelatedDeploy || m.Service == sess.PrimaryService {
			ic.DeployCorrelation = &DeployCorrelationContext{
				Service:     m.Service,
				CommitHash:  m.CommitHash,
				Environment: m.Environment,
				DeployedAt:  m.FirstSeenAt.Format(time.RFC3339),
			}
			break
		}
	}
}

func (ci *ContextInjector) getTrafficContext(ctx context.Context, sess *store.InvestigationSession, ic *InvestigationContext) {
	if ci.analyticsStore == nil {
		return
	}

	now := time.Now().UTC()
	summary, err := ci.analyticsStore.TrafficSummary(ctx, store.AnalyticsParams{
		Service: sess.PrimaryService,
		Since:   now.Add(-1 * time.Hour),
		Until:   now,
	})
	if err != nil || summary == nil {
		return
	}

	tc := &TrafficContext{
		TotalRequests: summary.TotalRequests,
		ErrorRate:     summary.ErrorRate,
	}

	// Detect anomalies by comparing to same hour last week via heatmap
	heatmap, err := ci.analyticsStore.TrafficHeatmap(ctx, sess.PrimaryService)
	if err == nil && len(heatmap) > 0 {
		currentHour := now.Hour()
		currentDay := int(now.Weekday())
		for _, cell := range heatmap {
			if cell.DayOfWeek == currentDay && cell.HourOfDay == currentHour && cell.RequestCount > 0 {
				ratio := float64(summary.TotalRequests) / float64(cell.RequestCount)
				if ratio > 1.5 {
					tc.Anomaly = fmt.Sprintf("Traffic %.1fx higher than usual for this hour", ratio)
				} else if ratio < 0.5 {
					tc.Anomaly = fmt.Sprintf("Traffic %.1fx lower than usual for this hour", ratio)
				}
				break
			}
		}
	}

	ic.TrafficContext = tc
}

func (ci *ContextInjector) getRecentAdminActions(ctx context.Context, sess *store.InvestigationSession, ic *InvestigationContext) {
	if ci.auditStore == nil {
		return
	}

	entries, err := ci.auditStore.Recent(ctx, 20)
	if err != nil {
		slog.Debug("context_injector: audit query failed", "error", err)
		return
	}

	cutoff := sess.StartedAt.Add(-1 * time.Hour)
	for _, e := range entries {
		if e.CreatedAt.Before(cutoff) {
			continue
		}
		ic.RecentAdminActions = append(ic.RecentAdminActions, AdminActionContext{
			Action:     e.Action,
			TargetType: e.TargetType,
			UserEmail:  e.UserEmail,
			CreatedAt:  e.CreatedAt.Format(time.RFC3339),
		})
		if len(ic.RecentAdminActions) >= 5 {
			break
		}
	}
}

// InjectContext adds investigation_context to a tool response map.
func (ci *ContextInjector) InjectContext(resp map[string]any, ic *InvestigationContext) {
	if ic != nil {
		resp["investigation_context"] = ic
	}
}

// InjectContextIntoResult parses, enriches, and re-serializes the tool result
// for investigation-intent sessions.
func InjectContextIntoResult(ci *ContextInjector, sess *store.InvestigationSession, toolName string, resultText string) string {
	if ci == nil || sess == nil || sess.Intent != IntentInvestigation {
		return resultText
	}

	var respMap map[string]any
	if err := json.Unmarshal([]byte(resultText), &respMap); err != nil {
		return resultText
	}

	ic := ci.BuildContext(context.Background(), sess, toolName)
	ci.InjectContext(respMap, ic)

	newData, err := json.Marshal(respMap)
	if err != nil {
		return resultText
	}
	return string(newData)
}

// FormatDeadEndWarning returns a human-readable warning string.
func FormatDeadEndWarning(w DeadEndWarning) string {
	return fmt.Sprintf("Warning: %s → %s — %s", w.FromTool, w.ToTool, w.Reason)
}
