package auth

import (
	"encoding/json"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/adham90/opentrace/internal/cryptoutil"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

// connectRequest is the JSON body for POST /api/auth/connect.
type connectRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// connectResponse is returned on successful authentication.
type connectResponse struct {
	Token        string        `json:"token"`
	User         connectUser   `json:"user"`
	ServerInfo   connectServer `json:"server"`
	CreatedAdmin bool          `json:"created_admin,omitempty"`
}

type connectUser struct {
	ID    string         `json:"id"`
	Email string         `json:"email"`
	Role  store.UserRole `json:"role"`
}

type connectServer struct {
	Version string `json:"version"`
	Name    string `json:"name"`
}

// handleConnect is the API endpoint for `npx opentrace connect`.
// It handles three cases:
//  1. Zero users exist → create admin account with provided credentials
//  2. Valid credentials → authenticate and return personal MCP token
//  3. Invalid credentials → reject
//
// POST /api/auth/connect
func (h *handler) handleConnect(w http.ResponseWriter, r *http.Request) {
	if h.userStore == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "auth not configured")
		return
	}

	var req connectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		server.WriteError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	ctx := r.Context()

	// Check how many users exist
	userCount, err := h.userStore.Count(ctx)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to check user count")
		return
	}

	if userCount == 0 {
		// First connection — create admin
		h.handleConnectCreateAdmin(w, r, req)
		return
	}

	// Existing users — authenticate
	h.handleConnectLogin(w, r, req)
}

// handleConnectCreateAdmin creates the first admin user.
func (h *handler) handleConnectCreateAdmin(w http.ResponseWriter, r *http.Request, req connectRequest) {
	ctx := r.Context()

	if len(req.Password) < 12 {
		server.WriteError(w, http.StatusBadRequest, "password must be at least 12 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	mcpToken, err := cryptoutil.GenerateMCPToken()
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	displayName := strings.Split(req.Email, "@")[0]

	user, err := h.userStore.Create(ctx, store.CreateUserParams{
		Email:        req.Email,
		PasswordHash: string(hash),
		DisplayName:  displayName,
		Role:         store.RoleAdmin,
		MCPToken:     &mcpToken,
	})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to create admin account")
		return
	}

	server.WriteJSON(w, http.StatusCreated, connectResponse{
		Token:        mcpToken,
		CreatedAdmin: true,
		User: connectUser{
			ID:    user.ID,
			Email: user.Email,
			Role:  user.Role,
		},
		ServerInfo: connectServer{
			Name: "opentrace",
		},
	})
}

// handleConnectLogin authenticates an existing user.
func (h *handler) handleConnectLogin(w http.ResponseWriter, r *http.Request, req connectRequest) {
	ctx := r.Context()

	// Check brute-force lockout
	if h.loginTracker.isLocked(req.Email) {
		server.WriteError(w, http.StatusTooManyRequests, "too many failed attempts — try again later")
		return
	}

	user, err := h.userStore.GetByEmail(ctx, req.Email)
	if err != nil {
		// Normalize timing to prevent email enumeration
		bcrypt.CompareHashAndPassword(
			[]byte("$2a$10$000000000000000000000uPUxFN4W/KAl5RjNmT5FXCVHS1FTqbWO"),
			[]byte(req.Password),
		)
		h.loginTracker.recordFailure(req.Email)
		server.WriteError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if !user.IsActive {
		h.loginTracker.recordFailure(req.Email)
		server.WriteError(w, http.StatusForbidden, "account is disabled")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		h.loginTracker.recordFailure(req.Email)
		server.WriteError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	h.loginTracker.recordSuccess(req.Email)

	// Generate a fresh MCP token for this connection
	mcpToken := ""
	if user.MCPToken != nil && *user.MCPToken != "" {
		mcpToken = *user.MCPToken
	} else {
		// User doesn't have an MCP token yet — generate one
		newToken, err := cryptoutil.GenerateMCPToken()
		if err != nil {
			server.WriteError(w, http.StatusInternalServerError, "failed to generate token")
			return
		}
		if err := h.userStore.UpdateMCPToken(ctx, user.ID, newToken); err != nil {
			server.WriteError(w, http.StatusInternalServerError, "failed to save token")
			return
		}
		mcpToken = newToken
	}

	// Ensure MCP is enabled for this user
	if !user.MCPEnabled {
		enabled := true
		if _, err := h.userStore.Update(ctx, user.ID, store.UpdateUserParams{MCPEnabled: &enabled}); err != nil {
			// Non-fatal — token still works via MCPTokenAuth
		}
	}

	server.WriteJSON(w, http.StatusOK, connectResponse{
		Token: mcpToken,
		User: connectUser{
			ID:    user.ID,
			Email: user.Email,
			Role:  user.Role,
		},
		ServerInfo: connectServer{
			Name: "opentrace",
		},
	})
}

// handleConnectCheck is a lightweight endpoint to test connectivity.
// GET /api/auth/connect — returns server info and whether setup is needed.
func (h *handler) handleConnectCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	needsSetup := false
	if h.userStore != nil {
		count, err := h.userStore.Count(ctx)
		if err == nil {
			needsSetup = count == 0
		}
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"server":      "opentrace",
		"needs_setup": needsSetup,
	})
}
