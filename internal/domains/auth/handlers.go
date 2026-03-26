package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/adham90/opentrace/internal/auth"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/views"
	webviews "github.com/adham90/opentrace/internal/web/views"
)

const (
	maxFailedLogins = 5
	lockoutDuration = 15 * time.Minute

	sessionDuration    = 24 * time.Hour
	rememberMeDuration = 7 * 24 * time.Hour
)

// handler holds the dependencies for auth HTTP handlers.
type handler struct {
	userStore    store.UserStore
	sessionStore store.SessionStore
	auditStore   store.AuditStore
	cfg          *config.Config

	loginTracker  *loginTracker
	secureCookies bool
}

// ── Login tracker (brute-force protection) ────────────────────

type loginAttempt struct {
	failures int
	lockedAt time.Time
}

type loginTracker struct {
	mu      sync.Mutex
	entries map[string]*loginAttempt
}

func newLoginTracker() *loginTracker {
	return &loginTracker{entries: make(map[string]*loginAttempt)}
}

func (lt *loginTracker) recordFailure(email string) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	entry, ok := lt.entries[email]
	if !ok {
		entry = &loginAttempt{}
		lt.entries[email] = entry
	}
	entry.failures++
	if entry.failures >= maxFailedLogins {
		entry.lockedAt = time.Now()
	}
}

func (lt *loginTracker) isLocked(email string) bool {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	entry, ok := lt.entries[email]
	if !ok {
		return false
	}
	if entry.lockedAt.IsZero() {
		return false
	}
	if time.Since(entry.lockedAt) > lockoutDuration {
		delete(lt.entries, email)
		return false
	}
	return true
}

func (lt *loginTracker) recordSuccess(email string) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	delete(lt.entries, email)
}

// ── Layout helper ─────────────────────────────────────────────

func (h *handler) layoutData(r *http.Request, title, nav string) views.LayoutData {
	user := server.UserFromContext(r.Context())
	isAdmin := user != nil && user.Role == store.RoleAdmin
	return views.LayoutData{
		Title:   title,
		Nav:     nav,
		User:    user,
		IsAdmin: isAdmin,
		DevMode: h.cfg != nil && h.cfg.DevMode,
	}
}

// ── Login ─────────────────────────────────────────────────────

func (h *handler) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	// Already logged in -> redirect to home
	if server.UserFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// No users exist -> redirect to onboarding
	if h.userStore != nil {
		count, err := h.userStore.Count(r.Context())
		if err == nil && count == 0 {
			http.Redirect(w, r, "/onboarding", http.StatusFound)
			return
		}
	}

	layout := h.layoutData(r, "Login", "")
	webviews.LoginPage(layout, "").Render(r.Context(), w)
}

func (h *handler) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if h.userStore == nil || h.sessionStore == nil {
		server.WriteError(w, http.StatusInternalServerError, "auth not configured")
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	if email == "" || password == "" {
		h.renderLoginError(w, r, "Email and password are required")
		return
	}

	// Per-account lockout check
	if h.loginTracker.isLocked(email) {
		h.renderLoginError(w, r, "Account temporarily locked due to too many failed attempts. Try again later.")
		return
	}

	user, err := h.userStore.GetByEmail(r.Context(), email)
	if err != nil {
		// Run a dummy bcrypt to normalize timing (prevents email enumeration)
		bcrypt.CompareHashAndPassword([]byte("$2a$10$000000000000000000000uPUxFN4W/KAl5RjNmT5FXCVHS1FTqbWO"), []byte(password))
		h.loginTracker.recordFailure(email)
		h.renderLoginError(w, r, "Invalid email or password")
		return
	}

	if !user.IsActive {
		h.renderLoginError(w, r, "Account is disabled")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		h.loginTracker.recordFailure(email)
		h.renderLoginError(w, r, "Invalid email or password")
		return
	}

	h.loginTracker.recordSuccess(email)

	// Create session
	token, err := auth.GenerateSessionToken()
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to generate session")
		return
	}

	duration := sessionDuration
	if r.FormValue("remember") == "on" {
		duration = rememberMeDuration
	}

	expiresAt := time.Now().Add(duration)
	_, err = h.sessionStore.Create(r.Context(), user.ID, token, expiresAt)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	auth.SetSessionCookie(w, token, int(duration.Seconds()), h.secureCookies)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *handler) renderLoginError(w http.ResponseWriter, r *http.Request, msg string) {
	layout := h.layoutData(r, "Login", "")
	w.WriteHeader(http.StatusBadRequest)
	webviews.LoginPage(layout, msg).Render(r.Context(), w)
}

// ── Register ──────────────────────────────────────────────────

func (h *handler) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if h.userStore == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// No users -> redirect to onboarding (registration is now admin-only for adding users)
	count, _ := h.userStore.Count(r.Context())
	if count == 0 {
		http.Redirect(w, r, "/onboarding", http.StatusFound)
		return
	}

	// Registration only allowed when admin is logged in
	user := server.UserFromContext(r.Context())
	if user == nil || user.Role != store.RoleAdmin {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	layout := h.layoutData(r, "Register", "")
	webviews.RegisterPage(layout, "").Render(r.Context(), w)
}

func (h *handler) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	if h.userStore == nil || h.sessionStore == nil {
		server.WriteError(w, http.StatusInternalServerError, "auth not configured")
		return
	}

	count, _ := h.userStore.Count(r.Context())
	currentUser := server.UserFromContext(r.Context())

	// Registration is only allowed when: no users exist, or admin is logged in
	if count > 0 && (currentUser == nil || currentUser.Role != store.RoleAdmin) {
		server.WriteError(w, http.StatusForbidden, "registration not allowed")
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	displayName := strings.TrimSpace(r.FormValue("display_name"))

	if email == "" || password == "" {
		h.renderRegisterError(w, r, "Email and password are required")
		return
	}
	if len(password) < 12 {
		h.renderRegisterError(w, r, "Password must be at least 12 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	// First user becomes admin
	role := store.RoleMember
	if count == 0 {
		role = store.RoleAdmin
	}

	if displayName == "" {
		displayName = strings.Split(email, "@")[0]
	}

	mcpToken, err := auth.GenerateMCPToken()
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to generate MCP token")
		return
	}

	user, err := h.userStore.Create(r.Context(), store.CreateUserParams{
		Email:        email,
		PasswordHash: string(hash),
		DisplayName:  displayName,
		Role:         role,
		MCPToken:     &mcpToken,
	})
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			h.renderRegisterError(w, r, "Email is already registered")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	// If this is the first user (self-registration), auto-login
	if currentUser == nil {
		token, err := auth.GenerateSessionToken()
		if err == nil {
			expiresAt := time.Now().Add(sessionDuration)
			h.sessionStore.Create(r.Context(), user.ID, token, expiresAt)
			auth.SetSessionCookie(w, token, int(sessionDuration.Seconds()), h.secureCookies)
		}
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// Admin created a new user -- redirect to users page
	server.Audit(r, h.auditStore, "user.create", "user", user.ID, fmt.Sprintf("email=%s role=%s", email, role))
	http.Redirect(w, r, "/users", http.StatusFound)
}

func (h *handler) renderRegisterError(w http.ResponseWriter, r *http.Request, msg string) {
	layout := h.layoutData(r, "Register", "")
	w.WriteHeader(http.StatusBadRequest)
	webviews.RegisterPage(layout, msg).Render(r.Context(), w)
}

// ── Logout ────────────────────────────────────────────────────

func (h *handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess := server.SessionFromContext(r.Context())
	if sess != nil && h.sessionStore != nil {
		h.sessionStore.Delete(r.Context(), sess.ID)
	}
	auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ── Profile ───────────────────────────────────────────────────

func (h *handler) handleProfilePage(w http.ResponseWriter, r *http.Request) {
	user := server.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	layout := h.layoutData(r, "Profile", "profile")
	webviews.ProfilePage(layout, user).Render(r.Context(), w)
}

func (h *handler) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := server.UserFromContext(r.Context())
	if user == nil {
		server.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")

	if currentPassword == "" || newPassword == "" {
		server.WriteError(w, http.StatusBadRequest, "current and new passwords are required")
		return
	}
	if len(newPassword) < 12 {
		server.WriteError(w, http.StatusBadRequest, "new password must be at least 12 characters")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
		server.WriteError(w, http.StatusBadRequest, "current password is incorrect")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	if err := h.userStore.UpdatePassword(r.Context(), user.ID, string(hash)); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	// Invalidate other sessions (keep current one)
	sess := server.SessionFromContext(r.Context())
	if err := h.sessionStore.DeleteAllForUser(r.Context(), user.ID); err != nil {
		slog.Warn("failed to delete sessions after password change", "user_id", user.ID, "error", err)
	}
	if sess != nil {
		token, _ := auth.GenerateSessionToken()
		expiresAt := time.Now().Add(sessionDuration)
		h.sessionStore.Create(r.Context(), user.ID, token, expiresAt)
		auth.SetSessionCookie(w, token, int(sessionDuration.Seconds()), h.secureCookies)
	}

	server.Audit(r, h.auditStore, "user.password_change", "user", user.ID, "")
	http.Redirect(w, r, "/profile?success=Password+changed", http.StatusFound)
}

// ── MCP Token (own) ───────────────────────────────────────────

func (h *handler) handleGetOwnMCPToken(w http.ResponseWriter, r *http.Request) {
	user := server.UserFromContext(r.Context())
	if user == nil {
		server.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !user.MCPEnabled || user.MCPToken == nil {
		server.WriteError(w, http.StatusForbidden, "MCP access is not enabled for your account")
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]string{"mcp_token": *user.MCPToken})
}

// ── Admin User Management ─────────────────────────────────────

func (h *handler) handleUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	var body struct {
		Role store.UserRole `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Role != store.RoleAdmin && body.Role != store.RoleMember {
		server.WriteError(w, http.StatusBadRequest, "role must be 'admin' or 'member'")
		return
	}

	updated, err := h.userStore.Update(r.Context(), userID, store.UpdateUserParams{Role: &body.Role})
	if err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			server.WriteError(w, http.StatusConflict, "cannot demote the last admin")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to update role")
		return
	}
	server.Audit(r, h.auditStore, "user.role_change", "user", userID, fmt.Sprintf("role=%s", body.Role))
	server.WriteJSON(w, http.StatusOK, updated)
}

func (h *handler) handleToggleMCPAccess(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updated, err := h.userStore.Update(r.Context(), userID, store.UpdateUserParams{MCPEnabled: &body.Enabled})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to toggle MCP access")
		return
	}
	server.WriteJSON(w, http.StatusOK, updated)
}

func (h *handler) handleToggleUserActive(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	var body struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updated, err := h.userStore.Update(r.Context(), userID, store.UpdateUserParams{IsActive: &body.Active})
	if err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			server.WriteError(w, http.StatusConflict, "cannot deactivate the last admin")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to toggle user active")
		return
	}

	// Invalidate sessions for deactivated user
	if !body.Active {
		if err := h.sessionStore.DeleteAllForUser(r.Context(), userID); err != nil {
			slog.Warn("failed to delete sessions for deactivated user", "user_id", userID, "error", err)
		}
	}

	server.Audit(r, h.auditStore, "user.toggle_active", "user", userID, fmt.Sprintf("active=%t", body.Active))
	server.WriteJSON(w, http.StatusOK, updated)
}

func (h *handler) handleRegenerateMCPToken(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	token, err := auth.GenerateMCPToken()
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	if err := h.userStore.UpdateMCPToken(r.Context(), userID, token); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to update token")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]string{"mcp_token": token})
}

func (h *handler) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	// Prevent self-deletion
	currentUser := server.UserFromContext(r.Context())
	if currentUser != nil && currentUser.ID == userID {
		server.WriteError(w, http.StatusConflict, "cannot delete your own account")
		return
	}

	if err := h.userStore.Delete(r.Context(), userID); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			server.WriteError(w, http.StatusConflict, "cannot delete the last admin")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}

	// Clean up sessions
	if err := h.sessionStore.DeleteAllForUser(r.Context(), userID); err != nil {
		slog.Warn("failed to delete sessions for deleted user", "user_id", userID, "error", err)
	}

	server.Audit(r, h.auditStore, "user.delete", "user", userID, "")
	server.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
