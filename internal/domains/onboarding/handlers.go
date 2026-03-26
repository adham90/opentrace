package onboarding

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/adham90/opentrace/internal/auth"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/managed"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/views"
	webviews "github.com/adham90/opentrace/internal/web/views"
)

const sessionDuration = 24 * time.Hour

type handler struct {
	userStore     store.UserStore
	sessionStore  store.SessionStore
	settingsStore store.SettingsStore
	cfg           *config.Config
	secureCookies bool
}

func (h *handler) layoutData(r *http.Request, title, nav string) views.LayoutData {
	user := server.UserFromContext(r.Context())
	isAdmin := user != nil && user.Role == store.RoleAdmin
	return views.LayoutData{
		Title:     title,
		Nav:       nav,
		User:      user,
		IsAdmin:   isAdmin,
		CSRFToken: server.CSRFToken(r.Context()),
		DevMode:   h.cfg != nil && h.cfg.DevMode,
	}
}

func (h *handler) handleOnboardingPage(w http.ResponseWriter, r *http.Request) {
	// In managed mode, the platform handles setup — skip onboarding.
	if managed.Enabled() {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	if h.userStore != nil {
		count, err := h.userStore.Count(r.Context())
		if err == nil && count > 0 {
			// Step 2: user just created, show API key + setup instructions
			if r.URL.Query().Get("step") == "2" && server.UserFromContext(r.Context()) != nil {
				h.renderOnboardingStep2(w, r)
				return
			}
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
	}

	layout := h.layoutData(r, "Setup", "")
	webviews.OnboardingPage(layout, 1, "", "", "").Render(r.Context(), w)
}

func (h *handler) handleOnboardingSubmit(w http.ResponseWriter, r *http.Request) {
	// If users already exist, redirect to home
	if h.userStore != nil {
		count, err := h.userStore.Count(r.Context())
		if err == nil && count > 0 {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
	}

	if h.userStore == nil || h.sessionStore == nil {
		server.WriteError(w, http.StatusInternalServerError, "auth not configured")
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	displayName := strings.TrimSpace(r.FormValue("display_name"))

	if email == "" || password == "" {
		h.renderOnboardingError(w, r, "Email and password are required")
		return
	}
	if len(password) < 12 {
		h.renderOnboardingError(w, r, "Password must be at least 12 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to hash password")
		return
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
		Role:         store.RoleAdmin,
		MCPToken:     &mcpToken,
	})
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			h.renderOnboardingError(w, r, "Email is already registered")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	// Generate and store API key (unless env var is already set)
	if h.cfg == nil || h.cfg.APIKey == "" {
		apiKey, err := auth.GenerateAPIKey()
		if err != nil {
			server.WriteError(w, http.StatusInternalServerError, "failed to generate API key")
			return
		}
		if h.settingsStore != nil {
			if err := h.settingsStore.SetAPIKey(r.Context(), apiKey); err != nil {
				server.WriteError(w, http.StatusInternalServerError, "failed to store API key")
				return
			}
		}
	}

	// Auto-login
	token, err := auth.GenerateSessionToken()
	if err == nil {
		expiresAt := time.Now().Add(sessionDuration)
		h.sessionStore.Create(r.Context(), user.ID, token, expiresAt)
		auth.SetSessionCookie(w, token, int(sessionDuration.Seconds()), h.secureCookies)
	}

	// PRG: redirect to step 2 so the URL reflects the state
	http.Redirect(w, r, "/onboarding?step=2", http.StatusFound)
}

func (h *handler) renderOnboardingStep2(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		scheme = fwd
	}
	baseURL := scheme + "://" + r.Host
	apiKey := h.getEffectiveAPIKey(r)

	layout := h.layoutData(r, "Setup Complete", "")
	webviews.OnboardingPage(layout, 2, "", apiKey, baseURL).Render(r.Context(), w)
}

func (h *handler) renderOnboardingError(w http.ResponseWriter, r *http.Request, msg string) {
	layout := h.layoutData(r, "Setup", "")
	w.WriteHeader(http.StatusBadRequest)
	webviews.OnboardingPage(layout, 1, msg, "", "").Render(r.Context(), w)
}

// getEffectiveAPIKey returns the API key from the env var (if set) or from the DB.
func (h *handler) getEffectiveAPIKey(r *http.Request) string {
	if h.cfg != nil && h.cfg.APIKey != "" {
		return h.cfg.APIKey
	}
	if h.settingsStore != nil {
		key, err := h.settingsStore.GetAPIKey(r.Context())
		if err == nil && key != "" {
			return key
		}
	}
	return ""
}
