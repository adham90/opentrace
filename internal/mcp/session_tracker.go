package mcp

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
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

// SetLastSuggestions records the suggestions returned to the client,
// used for detecting whether the next tool call was a suggestion acceptance.
func (st *SessionTracker) SetLastSuggestions(suggestions []ToolSuggestion) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.lastSuggestions = make([]ToolSuggestion, len(suggestions))
	copy(st.lastSuggestions, suggestions)
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
// Also records tool transitions and suggestion acceptance.
// Returns the current step index.
func (st *SessionTracker) RecordStep(toolName string, isError bool) int {
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
	}

	// Track suggestion acceptance
	st.trackSuggestionAcceptance(ctx, sessID, idx, toolName)

	// Record tool transition
	st.recordTransition(ctx, toolName, sessID, idx)

	return idx
}

// trackSuggestionAcceptance checks if the tool was in the last suggestions
// and records the result.
func (st *SessionTracker) trackSuggestionAcceptance(ctx context.Context, sessID string, stepIndex int, toolName string) {
	if st.activityStore == nil {
		return
	}

	st.mu.RLock()
	suggestions := st.lastSuggestions
	st.mu.RUnlock()

	wasSuggested := false
	rank := 0
	for i, s := range suggestions {
		if s.Tool == toolName {
			wasSuggested = true
			rank = i + 1
			break
		}
	}

	if err := st.activityStore.SetSuggestionTracking(ctx, sessID, stepIndex, wasSuggested, rank); err != nil {
		slog.Debug("failed to track suggestion acceptance", "error", err)
	}
}

// recordTransition records a tool-to-tool transition and updates the previous step's followed_by.
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
		// Update the previous step's followed_by
		if st.activityStore != nil {
			if err := st.activityStore.UpdateFollowedBy(ctx, sessID, stepIndex-1, toolName); err != nil {
				slog.Debug("failed to update followed_by", "error", err)
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
// It first infers the outcome if no summary was provided, then closes the session.
func (st *SessionTracker) CloseSession() {
	if st.store == nil {
		return
	}

	// Re-read the latest session state before finalizing.
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

		// Infer outcome if no manual summary was provided.
		finalizeSessionOutcome(st)
	}

	// Finalize transitions with outcome (for ranking score)
	if st.transitionStore != nil && sess != nil && sess.TotalSteps > 1 {
		outcome := string(sess.Status)
		for i := 0; i < len(sess.ToolSequence)-1; i++ {
			if err := st.transitionStore.IncrementWithOutcome(ctx, sess.ToolSequence[i], sess.ToolSequence[i+1], sess.Intent, outcome); err != nil {
				slog.Debug("failed to finalize transition outcome", "error", err)
			}
		}
	}

	// Clear session reference.
	st.mu.Lock()
	sess = st.session
	st.session = nil
	st.mu.Unlock()

	if sess == nil {
		return
	}

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
