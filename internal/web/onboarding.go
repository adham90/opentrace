package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/adham90/opentrace/internal/store"
)

type onboardingData struct {
	pageData
	Error   string
	Step    int
	BaseURL string
}

func (s *Server) handleOnboardingPage(w http.ResponseWriter, r *http.Request) {
	if s.userStore != nil {
		count, err := s.userStore.Count(r.Context())
		if err == nil && count > 0 {
			// Step 2: user just created, show API key + setup instructions
			if r.URL.Query().Get("step") == "2" && UserFromContext(r.Context()) != nil {
				s.renderOnboardingStep2(w, r)
				return
			}
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
	}

	data := onboardingData{
		pageData: s.newPageData(r, "Setup", ""),
		Step:     1,
	}
	tmpl := s.getTemplate(onboardingTmpl,
		"internal/web/templates/layout_minimal.html",
		"internal/web/templates/onboarding.html")
	tmpl.ExecuteTemplate(w, "layout_minimal", data)
}

func (s *Server) handleOnboardingSubmit(w http.ResponseWriter, r *http.Request) {
	// If users already exist, redirect to home
	if s.userStore != nil {
		count, err := s.userStore.Count(r.Context())
		if err == nil && count > 0 {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
	}

	if s.userStore == nil || s.sessionStore == nil {
		writeError(w, http.StatusInternalServerError, "auth not configured")
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	displayName := strings.TrimSpace(r.FormValue("display_name"))

	if email == "" || password == "" {
		s.renderOnboardingError(w, r, "Email and password are required")
		return
	}
	if len(password) < 12 {
		s.renderOnboardingError(w, r, "Password must be at least 12 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
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
		Role:         store.RoleAdmin,
		MCPToken:     &mcpToken,
	})
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			s.renderOnboardingError(w, r, "Email is already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	// Generate and store API key (unless env var is already set)
	var apiKey string
	if s.cfg == nil || s.cfg.APIKey == "" {
		apiKey, err = generateAPIKey()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate API key")
			return
		}
		if s.settingsStore != nil {
			if err := s.settingsStore.SetAPIKey(r.Context(), apiKey); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to store API key")
				return
			}
		}
	} else {
		apiKey = s.cfg.APIKey
	}

	// Auto-login
	token, err := generateSessionToken()
	if err == nil {
		expiresAt := time.Now().Add(sessionDuration)
		s.sessionStore.Create(r.Context(), user.ID, token, expiresAt)
		setSessionCookie(w, token, int(sessionDuration.Seconds()))
	}

	// PRG: redirect to step 2 so the URL reflects the state
	http.Redirect(w, r, "/onboarding?step=2", http.StatusFound)
}

func (s *Server) renderOnboardingStep2(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		scheme = fwd
	}
	baseURL := scheme + "://" + r.Host

	data := onboardingData{
		pageData: s.newPageData(r, "Setup Complete", ""),
		Step:     2,
		BaseURL:  baseURL,
	}
	data.pageData.APIKey = s.getEffectiveAPIKey(r.Context())

	tmpl := s.getTemplate(onboardingTmpl,
		"internal/web/templates/layout_minimal.html",
		"internal/web/templates/onboarding.html")
	tmpl.ExecuteTemplate(w, "layout_minimal", data)
}

func (s *Server) renderOnboardingError(w http.ResponseWriter, r *http.Request, msg string) {
	data := onboardingData{
		pageData: s.newPageData(r, "Setup", ""),
		Error:    msg,
		Step:     1,
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	tmpl := s.getTemplate(onboardingTmpl,
		"internal/web/templates/layout_minimal.html",
		"internal/web/templates/onboarding.html")
	tmpl.ExecuteTemplate(w, "layout_minimal", data)
}
