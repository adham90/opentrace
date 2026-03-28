package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/pkg/store"
)

// sessionContextKey is the context key for the current investigation session.
type sessionContextKey struct{}

// SessionTracker manages MCP investigation session lifecycle.
// It creates sessions on initialize, tracks steps per tool call,
// and closes sessions on shutdown/disconnect.
type SessionTracker struct {
	store           store.InvestigationSessionStore
	transitionStore store.ToolTransitionStore
	activityStore   store.MCPActivityStore
	user            *store.User    // authenticated user (may be nil for backward compat)
	transport       string         // "stdio" or "sse"
	workspace       string         // project path if available
	ctx             context.Context

	// Stage 4: additional stores for lifecycle hooks
	noteStore        store.AgentNoteStore
	runbookStore     store.RunbookEffectivenessStore
	queryMemoryStore store.QueryMemoryStore
	trendStore       store.TrendStore
	auditStore       store.AuditStore

	// Stage 5: code entity + deploy stores
	codeEntityStore store.CodeEntityStore
	errorGroupStore store.ErrorGroupStore
	deployStore     store.DeployStore

	// Stage 6: event + test correlation stores
	eventStore           store.EventStore
	testCorrelationStore store.TestCorrelationStore

	mu              sync.RWMutex
	session         *store.InvestigationSession
	connectionID    string // from OnRegisterSession
	step            atomic.Int64
	lastSuggestions []ToolSuggestion // tracks last suggestions served
}

// NewSessionTracker creates a new SessionTracker.
func NewSessionTracker(ctx context.Context, sessionStore store.InvestigationSessionStore, user *store.User, transport string) *SessionTracker {
	return &SessionTracker{
		store:     sessionStore,
		user:      user,
		transport: transport,
		ctx:       ctx,
	}
}

// SetTransitionStore configures the transition store for recording tool transitions.
func (st *SessionTracker) SetTransitionStore(ts store.ToolTransitionStore) {
	st.transitionStore = ts
}

// SetActivityStore configures the activity store for suggestion tracking.
func (st *SessionTracker) SetActivityStore(as store.MCPActivityStore) {
	st.activityStore = as
}

// SetNoteStore configures the note store for auto-note creation.
func (st *SessionTracker) SetNoteStore(ns store.AgentNoteStore) {
	st.noteStore = ns
}

// SetRunbookStore configures the runbook effectiveness store.
func (st *SessionTracker) SetRunbookStore(rs store.RunbookEffectivenessStore) {
	st.runbookStore = rs
}

// SetQueryMemoryStore configures the query memory store.
func (st *SessionTracker) SetQueryMemoryStore(qs store.QueryMemoryStore) {
	st.queryMemoryStore = qs
}

// SetTrendStore configures the trend store for deploy correlation.
func (st *SessionTracker) SetTrendStore(ts store.TrendStore) {
	st.trendStore = ts
}

// SetAuditStore configures the audit store for recording investigation completions.
func (st *SessionTracker) SetAuditStore(as store.AuditStore) {
	st.auditStore = as
}

// SetCodeEntityStore configures the code entity store for linking investigations.
func (st *SessionTracker) SetCodeEntityStore(ces store.CodeEntityStore) {
	st.codeEntityStore = ces
}

// SetErrorGroupStore configures the error group store for entity resolution.
func (st *SessionTracker) SetErrorGroupStore(egs store.ErrorGroupStore) {
	st.errorGroupStore = egs
}

// SetDeployStore configures the deploy store for linking investigations.
func (st *SessionTracker) SetDeployStore(ds store.DeployStore) {
	st.deployStore = ds
}

// SetEventStore configures the event store for session tracking.
func (st *SessionTracker) SetEventStore(es store.EventStore) {
	st.eventStore = es
}

// SetTestCorrelationStore configures the test correlation store.
func (st *SessionTracker) SetTestCorrelationStore(tcs store.TestCorrelationStore) {
	st.testCorrelationStore = tcs
}

// TrackFileRead adds a file path to the session's files_read list.
func (st *SessionTracker) TrackFileRead(filePath string) {
	st.mu.RLock()
	sess := st.session
	st.mu.RUnlock()
	if sess == nil || st.store == nil {
		return
	}

	// Append to existing list
	files := append(sess.FilesRead, filePath)
	// Deduplicate
	files = deduplicateStrings(files)
	st.UpdateSession(store.UpdateInvestigationSessionParams{
		FilesRead: files,
	})
	st.mu.Lock()
	if st.session != nil {
		st.session.FilesRead = files
	}
	st.mu.Unlock()
}

// TrackFileModified adds a file path to the session's files_modified list.
func (st *SessionTracker) TrackFileModified(filePath string) {
	st.mu.RLock()
	sess := st.session
	st.mu.RUnlock()
	if sess == nil || st.store == nil {
		return
	}

	files := append(sess.FilesModified, filePath)
	files = deduplicateStrings(files)
	st.UpdateSession(store.UpdateInvestigationSessionParams{
		FilesModified: files,
	})
	st.mu.Lock()
	if st.session != nil {
		st.session.FilesModified = files
	}
	st.mu.Unlock()
}

func deduplicateStrings(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// SetLastSuggestions records the suggestions returned to the client,
// used for detecting whether the next tool call was a suggestion acceptance.
func (st *SessionTracker) SetLastSuggestions(suggestions []ToolSuggestion) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.lastSuggestions = make([]ToolSuggestion, len(suggestions))
	copy(st.lastSuggestions, suggestions)
}

// SnapshotLastSuggestions returns a copy of the current lastSuggestions.
// Call this BEFORE the tool handler runs to capture what was suggested
// prior to the handler overwriting lastSuggestions with its own suggestions.
func (st *SessionTracker) SnapshotLastSuggestions() []ToolSuggestion {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make([]ToolSuggestion, len(st.lastSuggestions))
	copy(out, st.lastSuggestions)
	return out
}

// RegisterHooks wires the session tracker into the mcp-go hooks system.
func (st *SessionTracker) RegisterHooks(hooks *server.Hooks) {
	hooks.AddOnRegisterSession(st.onSessionRegistered)
	hooks.AddAfterInitialize(st.onInitialize)
	hooks.AddOnUnregisterSession(st.onDisconnect)
}

// CurrentSession returns the current investigation session, or nil if none.
func (st *SessionTracker) CurrentSession() *store.InvestigationSession {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.session
}

// CurrentSessionID returns the current session ID, or empty string if none.
func (st *SessionTracker) CurrentSessionID() string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.session != nil {
		return st.session.ID
	}
	return ""
}

// UserID returns the authenticated user ID, or empty string.
func (st *SessionTracker) UserID() string {
	if st.user != nil {
		return st.user.ID
	}
	return ""
}

// RecordStep increments step count and records the tool call in the session.
// On the first step, also classifies the session intent.
// Also records tool transitions.
// priorSuggestions is accepted for API compatibility but suggestion tracking
// is now handled at the INSERT level in wrapWithActivityLog.
// Returns the current step index.
func (st *SessionTracker) RecordStep(toolName string, isError bool, priorSuggestions []ToolSuggestion) int {
	idx := int(st.step.Add(1))

	sessID := st.CurrentSessionID()
	if sessID == "" || st.store == nil {
		return idx
	}

	ctx, cancel := context.WithTimeout(st.ctx, 5*time.Second)
	defer cancel()

	if err := st.store.RecordStep(ctx, sessID, toolName, isError); err != nil {
		slog.Warn("failed to record investigation step",
			"error", err,
			"session_id", sessID,
			"tool", toolName,
		)
	}

	// Classify intent on first tool call (Layer 3: tool-based fallback).
	// This may be overridden later if the tool includes a context parameter.
	if idx == 1 {
		st.classifyIntentFromTool(toolName)
		// Correlate deploy on first step where service is known
		st.correlateDeploy(ctx)
	}

	// Record tool transition
	st.recordTransition(ctx, toolName, sessID, idx)

	return idx
}

// recordTransition records a tool-to-tool transition in the tool_transitions table.
// Note: followed_by on mcp_activity is now updated by the async activity logger
// (in the store's Log method) to avoid races with the async INSERT.
func (st *SessionTracker) recordTransition(ctx context.Context, toolName string, sessID string, stepIndex int) {
	st.mu.RLock()
	sess := st.session
	st.mu.RUnlock()
	if sess == nil {
		return
	}

	// Record transition from previous tool
	if stepIndex > 1 && len(sess.ToolSequence) >= 1 {
		prevTool := sess.ToolSequence[len(sess.ToolSequence)-1]
		// The session's ToolSequence hasn't been updated with the current tool yet
		// at this point (RecordStep in the store appends it), so the last element
		// is the previous tool.
		if st.transitionStore != nil {
			if err := st.transitionStore.Increment(ctx, prevTool, toolName, sess.Intent); err != nil {
				slog.Debug("failed to record tool transition", "error", err)
			}
		}
	}
}

// ClassifyIntentFromContext updates the session intent based on a context
// string provided by the MCP client. This is Layer 2 (high confidence)
// and overrides the tool-based classification.
func (st *SessionTracker) ClassifyIntentFromContext(contextStr string, toolName string) {
	if contextStr == "" {
		return
	}

	st.mu.RLock()
	sess := st.session
	st.mu.RUnlock()
	if sess == nil {
		return
	}

	intent, detail := ClassifyIntent(contextStr, toolName)

	// Only update if this is a higher confidence classification.
	// Context-based always overrides tool-based.
	if sess.Intent == "" || sess.IntentDetail == "" {
		st.UpdateSession(store.UpdateInvestigationSessionParams{
			Intent:       &intent,
			IntentDetail: &detail,
		})
	}
}

// classifyIntentFromTool sets initial intent based on the first tool called.
func (st *SessionTracker) classifyIntentFromTool(toolName string) {
	intent, detail := classifyFromTool(toolName)
	st.UpdateSession(store.UpdateInvestigationSessionParams{
		Intent:       &intent,
		IntentDetail: &detail,
	})
}

// CloseSession closes the current session if open. Called on shutdown/disconnect.
// Non-critical lifecycle hooks (notes, runbook tracking, entity linking) run
// asynchronously to avoid blocking the response path.
func (st *SessionTracker) CloseSession() {
	if st.store == nil {
		return
	}

	st.mu.RLock()
	sess := st.session
	st.mu.RUnlock()

	if sess == nil {
		return
	}

	// Refresh from DB to get latest tool_sequence, total_steps etc.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	latest, err := st.store.GetByID(ctx, sess.ID)
	if err == nil && latest != nil {
		st.mu.Lock()
		st.session = latest
		st.mu.Unlock()
		sess = latest

		// Infer outcome if no manual summary was provided.
		finalizeSessionOutcome(st)
	}

	// Clear session reference early so new sessions can start.
	st.mu.Lock()
	sess = st.session
	st.session = nil
	st.mu.Unlock()

	if sess == nil {
		return
	}

	// Critical path: close the session in DB (must complete).
	if closeErr := st.store.Close(ctx, sess.ID); closeErr != nil {
		slog.Warn("failed to close investigation session",
			"error", closeErr,
			"session_id", sess.ID,
		)
	} else {
		slog.Info("investigation session closed",
			"session_id", sess.ID,
			"user_id", sess.UserID,
			"steps", st.step.Load(),
			"status", sess.Status,
		)
	}

	// Non-critical path: lifecycle hooks run async to avoid blocking.
	go st.runCloseHooksAsync(sess)
}

// runCloseHooksAsync runs non-critical lifecycle hooks in a background goroutine.
func (st *SessionTracker) runCloseHooksAsync(sess *store.InvestigationSession) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Finalize transitions with outcome (for ranking score)
	if st.transitionStore != nil && sess.TotalSteps > 1 {
		outcome := string(sess.Status)
		for i := 0; i < len(sess.ToolSequence)-1; i++ {
			if err := st.transitionStore.IncrementWithOutcome(ctx, sess.ToolSequence[i], sess.ToolSequence[i+1], sess.Intent, outcome); err != nil {
				slog.Debug("failed to finalize transition outcome", "error", err)
			}
		}
	}

	// Lifecycle hooks
	st.createAutoNotes(ctx, sess)
	st.updateRunbookEffectiveness(ctx, sess)
	st.updateQueryMemory(ctx, sess)
	st.recordAuditEntry(ctx, sess)

	// Link code entities from investigated errors
	LinkInvestigationToEntities(ctx, st.codeEntityStore, st.errorGroupStore, sess)
}

// UpdateSession applies updates to the current session.
func (st *SessionTracker) UpdateSession(params store.UpdateInvestigationSessionParams) {
	sessID := st.CurrentSessionID()
	if sessID == "" || st.store == nil {
		return
	}

	ctx, cancel := context.WithTimeout(st.ctx, 5*time.Second)
	defer cancel()

	if err := st.store.Update(ctx, sessID, params); err != nil {
		slog.Warn("failed to update investigation session",
			"error", err,
			"session_id", sessID,
		)
	}
}

// onSessionRegistered captures the connection ID when the MCP session is first registered.
func (st *SessionTracker) onSessionRegistered(_ context.Context, clientSession server.ClientSession) {
	st.mu.Lock()
	st.connectionID = clientSession.SessionID()
	st.mu.Unlock()
}

// onInitialize is called after the MCP Initialize handshake completes.
func (st *SessionTracker) onInitialize(_ context.Context, _ any, req *mcplib.InitializeRequest, _ *mcplib.InitializeResult) {
	if st.store == nil {
		return
	}

	st.mu.RLock()
	connID := st.connectionID
	st.mu.RUnlock()

	params := store.CreateInvestigationSessionParams{
		Transport:    st.transport,
		ConnectionID: connID,
	}

	// Extract client info from InitializeRequest.
	if req != nil {
		params.ClientName = req.Params.ClientInfo.Name
		params.ClientVersion = req.Params.ClientInfo.Version
	}

	// Extract user identity.
	if st.user != nil {
		params.UserID = st.user.ID
		params.UserEmail = st.user.Email
		params.UserRole = string(st.user.Role)
	}

	createCtx, cancel := context.WithTimeout(st.ctx, 10*time.Second)
	defer cancel()

	sess, err := st.store.Create(createCtx, params)
	if err != nil {
		slog.Error("failed to create investigation session",
			"error", err,
			"user_id", params.UserID,
		)
		return
	}

	st.mu.Lock()
	st.session = sess
	st.mu.Unlock()

	slog.Info("investigation session created",
		"session_id", sess.ID,
		"user_id", sess.UserID,
		"client", sess.ClientName,
		"transport", sess.Transport,
	)
}

// onDisconnect is called when an MCP session is unregistered (connection closes).
func (st *SessionTracker) onDisconnect(_ context.Context, _ server.ClientSession) {
	st.CloseSession()
}

// --- Stage 4: Lifecycle hooks ---

// createAutoNotes creates agent notes when a session is resolved with a summary.
func (st *SessionTracker) createAutoNotes(ctx context.Context, sess *store.InvestigationSession) {
	if st.noteStore == nil || sess == nil || sess.Status != store.InvestigationStatusResolved || sess.Summary == "" {
		return
	}

	var autoNoteIDs []string

	// Create note for the service
	if sess.PrimaryService != "" {
		noteText := fmt.Sprintf("Investigation resolved: %s", sess.Summary)
		if sess.RootCause != "" {
			noteText += fmt.Sprintf("\nRoot cause: %s", sess.RootCause)
		}
		if sess.FixDescription != "" {
			noteText += fmt.Sprintf("\nFix: %s", sess.FixDescription)
		}

		note, err := st.noteStore.Upsert(ctx, "service", sess.PrimaryService, noteText)
		if err != nil {
			slog.Debug("failed to create auto-note for service", "error", err)
		} else if note != nil {
			autoNoteIDs = append(autoNoteIDs, fmt.Sprintf("%d", note.ID))
		}
	}

	// Create notes for resolved error fingerprints
	for _, fp := range sess.ResolvedErrorGroupIDs {
		noteText := fmt.Sprintf("Error resolved in session %s: %s", sess.ID[:8], sess.Summary)
		if sess.RootCause != "" {
			noteText += fmt.Sprintf("\nRoot cause: %s", sess.RootCause)
		}
		note, err := st.noteStore.Upsert(ctx, "error", fp, noteText)
		if err != nil {
			slog.Debug("failed to create auto-note for error", "error", err, "fingerprint", fp)
		} else if note != nil {
			autoNoteIDs = append(autoNoteIDs, fmt.Sprintf("%d", note.ID))
		}
	}

	if len(autoNoteIDs) > 0 {
		st.store.Update(ctx, sess.ID, store.UpdateInvestigationSessionParams{
			AutoNoteIDs: autoNoteIDs,
		})
	}
}

// updateRunbookEffectiveness updates runbook stats based on session outcome.
func (st *SessionTracker) updateRunbookEffectiveness(ctx context.Context, sess *store.InvestigationSession) {
	if st.runbookStore == nil || sess == nil || len(sess.RunbooksExecuted) == 0 {
		return
	}

	outcome := string(sess.Status)
	if outcome != "resolved" && outcome != "abandoned" && outcome != "unresolved" {
		return
	}
	if outcome == "unresolved" {
		outcome = "abandoned"
	}

	for _, rb := range sess.RunbooksExecuted {
		if err := st.runbookStore.UpdateOutcome(ctx, store.UpdateRunbookEffectivenessParams{
			RunbookName: rb,
			Outcome:     outcome,
			StepsAfter:  sess.TotalSteps,
			DurationSec: sess.DurationSeconds,
		}); err != nil {
			slog.Debug("failed to update runbook effectiveness", "error", err, "runbook", rb)
		}
	}
}

// updateQueryMemory updates query memory for explained queries in the session.
func (st *SessionTracker) updateQueryMemory(ctx context.Context, sess *store.InvestigationSession) {
	if st.queryMemoryStore == nil || sess == nil || len(sess.ExplainedQueries) == 0 {
		return
	}

	rootCause := ""
	fix := ""
	if sess.Status == store.InvestigationStatusResolved {
		rootCause = sess.RootCause
		fix = sess.FixDescription
	}

	for _, fp := range sess.ExplainedQueries {
		if err := st.queryMemoryStore.Upsert(ctx, store.UpsertQueryMemoryParams{
			Fingerprint: fp,
			SessionID:   sess.ID,
			RootCause:   rootCause,
			Fix:         fix,
		}); err != nil {
			slog.Debug("failed to update query memory", "error", err, "fingerprint", fp)
		}
	}
}

// recordAuditEntry logs an audit event for significant investigations.
func (st *SessionTracker) recordAuditEntry(ctx context.Context, sess *store.InvestigationSession) {
	if st.auditStore == nil || sess == nil || sess.Intent != IntentInvestigation || sess.TotalSteps < 3 {
		return
	}

	details, _ := json.Marshal(map[string]any{
		"status":   sess.Status,
		"steps":    sess.TotalSteps,
		"service":  sess.PrimaryService,
		"duration": sess.DurationSeconds,
	})

	if err := st.auditStore.Log(ctx, store.LogAuditParams{
		UserID:     sess.UserID,
		UserEmail:  sess.UserEmail,
		Action:     "investigation_completed",
		TargetType: "investigation_session",
		TargetID:   sess.ID,
		Details:    string(details),
	}); err != nil {
		slog.Debug("failed to record audit entry for investigation", "error", err)
	}
}

// correlateDeploy checks for recent deploys when the session's primary service is set.
func (st *SessionTracker) correlateDeploy(ctx context.Context) {
	if st.trendStore == nil {
		return
	}

	st.mu.RLock()
	sess := st.session
	st.mu.RUnlock()
	if sess == nil || sess.PrimaryService == "" || sess.CorrelatedDeploy != "" {
		return
	}

	markers, err := st.trendStore.ListDeployMarkers(ctx, sess.PrimaryService, sess.StartedAt.Add(-30*time.Minute))
	if err != nil || len(markers) == 0 {
		return
	}

	// Take the most recent deploy
	latest := markers[0]
	for _, m := range markers[1:] {
		if m.FirstSeenAt.After(latest.FirstSeenAt) {
			latest = m
		}
	}

	deploy := latest.CommitHash
	st.UpdateSession(store.UpdateInvestigationSessionParams{
		CorrelatedDeploy: &deploy,
	})

	// Stage 5: link investigation to deploy record
	if st.deployStore != nil && sess != nil {
		d, err := st.deployStore.GetByCommit(ctx, deploy)
		if err == nil && d != nil {
			if linkErr := st.deployStore.LinkInvestigation(ctx, d.ID, sess.ID); linkErr != nil {
				slog.Debug("failed to link investigation to deploy", "error", linkErr)
			}
		}
	}
}
