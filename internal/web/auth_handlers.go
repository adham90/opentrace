package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/adham90/opentrace/internal/store"
)

// authPageData extends pageData with auth-specific fields.
type authPageData struct {
	pageData
	Error   string
	Success string
	Users   []store.User
}

const sessionDuration = 7 * 24 * time.Hour // 7 days

// --- Login ---

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	// Already logged in → redirect to home
	if UserFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// No users exist → redirect to onboarding
	if s.userStore != nil {
		count, err := s.userStore.Count(r.Context())
		if err == nil && count == 0 {
			http.Redirect(w, r, "/onboarding", http.StatusFound)
			return
		}
	}

	data := authPageData{pageData: s.newPageData(r, "Login", "")}
	tmpl := s.getTemplate(loginTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/login.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if s.userStore == nil || s.sessionStore == nil {
		writeError(w, http.StatusInternalServerError, "auth not configured")
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	if email == "" || password == "" {
		s.renderLoginError(w, r, "Email and password are required")
		return
	}

	user, err := s.userStore.GetByEmail(r.Context(), email)
	if err != nil {
		s.renderLoginError(w, r, "Invalid email or password")
		return
	}

	if !user.IsActive {
		s.renderLoginError(w, r, "Account is disabled")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		s.renderLoginError(w, r, "Invalid email or password")
		return
	}

	// Create session
	token, err := generateSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate session")
		return
	}

	expiresAt := time.Now().Add(sessionDuration)
	_, err = s.sessionStore.Create(r.Context(), user.ID, token, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	setSessionCookie(w, token, int(sessionDuration.Seconds()))
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) renderLoginError(w http.ResponseWriter, r *http.Request, msg string) {
	data := authPageData{
		pageData: s.newPageData(r, "Login", ""),
		Error:    msg,
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	tmpl := s.getTemplate(loginTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/login.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

// --- Register ---

func (s *Server) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if s.userStore == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// No users → redirect to onboarding (registration is now admin-only for adding users)
	count, _ := s.userStore.Count(r.Context())
	if count == 0 {
		http.Redirect(w, r, "/onboarding", http.StatusFound)
		return
	}

	// Registration only allowed when admin is logged in
	user := UserFromContext(r.Context())
	if user == nil || user.Role != store.RoleAdmin {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	data := authPageData{pageData: s.newPageData(r, "Register", "")}
	tmpl := s.getTemplate(registerTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/register.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	if s.userStore == nil || s.sessionStore == nil {
		writeError(w, http.StatusInternalServerError, "auth not configured")
		return
	}

	count, _ := s.userStore.Count(r.Context())
	currentUser := UserFromContext(r.Context())

	// Registration is only allowed when: no users exist, or admin is logged in
	if count > 0 && (currentUser == nil || currentUser.Role != store.RoleAdmin) {
		writeError(w, http.StatusForbidden, "registration not allowed")
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	displayName := strings.TrimSpace(r.FormValue("display_name"))

	if email == "" || password == "" {
		s.renderRegisterError(w, r, "Email and password are required")
		return
	}
	if len(password) < 12 {
		s.renderRegisterError(w, r, "Password must be at least 12 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
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

	mcpToken, err := generateMCPToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate MCP token")
		return
	}

	user, err := s.userStore.Create(r.Context(), store.CreateUserParams{
		Email:        email,
		PasswordHash: string(hash),
		DisplayName:  displayName,
		Role:         role,
		MCPToken:     &mcpToken,
	})
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			s.renderRegisterError(w, r, "Email is already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	// If this is the first user (self-registration), auto-login
	if currentUser == nil {
		token, err := generateSessionToken()
		if err == nil {
			expiresAt := time.Now().Add(sessionDuration)
			s.sessionStore.Create(r.Context(), user.ID, token, expiresAt)
			setSessionCookie(w, token, int(sessionDuration.Seconds()))
		}
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// Admin created a new user — redirect to user management
	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

func (s *Server) renderRegisterError(w http.ResponseWriter, r *http.Request, msg string) {
	data := authPageData{
		pageData: s.newPageData(r, "Register", ""),
		Error:    msg,
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	tmpl := s.getTemplate(registerTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/register.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

// --- Logout ---

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess := SessionFromContext(r.Context())
	if sess != nil && s.sessionStore != nil {
		s.sessionStore.Delete(r.Context(), sess.ID)
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

// --- Profile ---

func (s *Server) handleProfilePage(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	data := authPageData{pageData: s.newPageData(r, "Profile", "profile")}
	if msg := r.URL.Query().Get("success"); msg != "" {
		data.Success = msg
	}
	tmpl := s.getTemplate(profileTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/profile.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")

	if currentPassword == "" || newPassword == "" {
		writeError(w, http.StatusBadRequest, "current and new passwords are required")
		return
	}
	if len(newPassword) < 12 {
		writeError(w, http.StatusBadRequest, "new password must be at least 12 characters")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
		writeError(w, http.StatusBadRequest, "current password is incorrect")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	if err := s.userStore.UpdatePassword(r.Context(), user.ID, string(hash)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	// Invalidate other sessions (keep current one)
	sess := SessionFromContext(r.Context())
	s.sessionStore.DeleteAllForUser(r.Context(), user.ID)
	if sess != nil {
		token, _ := generateSessionToken()
		expiresAt := time.Now().Add(sessionDuration)
		s.sessionStore.Create(r.Context(), user.ID, token, expiresAt)
		setSessionCookie(w, token, int(sessionDuration.Seconds()))
	}

	http.Redirect(w, r, "/profile?success=Password+changed", http.StatusFound)
}

// --- MCP Token (own) ---

func (s *Server) handleGetOwnMCPToken(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !user.MCPEnabled || user.MCPToken == nil {
		writeError(w, http.StatusForbidden, "MCP access is not enabled for your account")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mcp_token": *user.MCPToken})
}

// --- Admin User Management ---

func (s *Server) handleUsersPage(w http.ResponseWriter, r *http.Request) {
	users, err := s.userStore.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	data := authPageData{
		pageData: s.newPageData(r, "Users", "users"),
		Users:    users,
	}
	tmpl := s.getTemplate(usersTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/users.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	var body struct {
		Role store.UserRole `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Role != store.RoleAdmin && body.Role != store.RoleMember {
		writeError(w, http.StatusBadRequest, "role must be 'admin' or 'member'")
		return
	}

	updated, err := s.userStore.Update(r.Context(), userID, store.UpdateUserParams{Role: &body.Role})
	if err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			writeError(w, http.StatusConflict, "cannot demote the last admin")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update role")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleToggleMCPAccess(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updated, err := s.userStore.Update(r.Context(), userID, store.UpdateUserParams{MCPEnabled: &body.Enabled})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to toggle MCP access")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleToggleUserActive(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	var body struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updated, err := s.userStore.Update(r.Context(), userID, store.UpdateUserParams{IsActive: &body.Active})
	if err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			writeError(w, http.StatusConflict, "cannot deactivate the last admin")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to toggle user active")
		return
	}

	// Invalidate sessions for deactivated user
	if !body.Active {
		s.sessionStore.DeleteAllForUser(r.Context(), userID)
	}

	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleRegenerateMCPToken(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	token, err := generateMCPToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	if err := s.userStore.UpdateMCPToken(r.Context(), userID, token); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"mcp_token": token})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	// Prevent self-deletion
	currentUser := UserFromContext(r.Context())
	if currentUser != nil && currentUser.ID == userID {
		writeError(w, http.StatusConflict, "cannot delete your own account")
		return
	}

	if err := s.userStore.Delete(r.Context(), userID); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			writeError(w, http.StatusConflict, "cannot delete the last admin")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}

	// Clean up sessions
	s.sessionStore.DeleteAllForUser(r.Context(), userID)

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
