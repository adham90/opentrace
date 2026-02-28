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
	store     store.InvestigationSessionStore
	user      *store.User    // authenticated user (may be nil for backward compat)
	transport string         // "stdio" or "sse"
	workspace string         // project path if available
	ctx       context.Context

	mu           sync.RWMutex
	session      *store.InvestigationSession
	connectionID string // from OnRegisterSession
	step         atomic.Int64
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

	return idx
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
