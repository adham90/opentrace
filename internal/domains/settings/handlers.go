package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/views"
	webviews "github.com/adham90/opentrace/internal/web/views"
)

type handler struct {
	settingsStore store.SettingsStore
	auditStore    store.AuditStore
	userStore     store.UserStore
	cfg           *config.Config
	db            *sql.DB
}

// effectiveAPIKey returns the API key from env var (if set) or from the DB.
func (h *handler) effectiveAPIKey(ctx context.Context) string {
	if h.cfg != nil && h.cfg.APIKey != "" {
		return h.cfg.APIKey
	}
	if h.settingsStore != nil {
		key, err := h.settingsStore.GetAPIKey(ctx)
		if err == nil && key != "" {
			return key
		}
	}
	return ""
}

func (h *handler) getRetention(w http.ResponseWriter, r *http.Request) {
	settings, err := h.settingsStore.GetRetention(r.Context())
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get retention settings")
		return
	}
	server.WriteJSON(w, http.StatusOK, settings)
}

func (h *handler) updateRetention(w http.ResponseWriter, r *http.Request) {
	var req store.RetentionSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.RetentionDays < 0 {
		server.WriteError(w, http.StatusBadRequest, "retention_days must be >= 0")
		return
	}
	if req.MetricRetentionDays < 0 {
		server.WriteError(w, http.StatusBadRequest, "metric_retention_days must be >= 0")
		return
	}
	if err := h.settingsStore.SetRetention(r.Context(), req); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to save retention settings")
		return
	}
	server.WriteJSON(w, http.StatusOK, req)
}

func (h *handler) getAPIKey(w http.ResponseWriter, r *http.Request) {
	envOverride := h.cfg != nil && h.cfg.APIKey != ""
	apiKey := h.effectiveAPIKey(r.Context())

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"api_key":      apiKey,
		"env_override": envOverride,
	})
}

func (h *handler) regenerateAPIKey(w http.ResponseWriter, r *http.Request) {
	// Block regeneration if env var override is active
	if h.cfg != nil && h.cfg.APIKey != "" {
		server.WriteError(w, http.StatusConflict, "API key is set via OPENTRACE_API_KEY environment variable and cannot be regenerated from the UI")
		return
	}

	key, err := server.GenerateAPIKey()
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to generate API key")
		return
	}

	if h.settingsStore == nil {
		server.WriteError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}

	if err := h.settingsStore.SetAPIKey(r.Context(), key); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to store API key")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]string{"api_key": key})
}

func (h *handler) getCORSOrigins(w http.ResponseWriter, r *http.Request) {
	envOverride := h.cfg != nil && len(h.cfg.CORSAllowedOrigins) > 0

	var origins string
	if envOverride {
		origins = strings.Join(h.cfg.CORSAllowedOrigins, ",")
	} else if h.settingsStore != nil {
		val, err := h.settingsStore.GetCORSOrigins(r.Context())
		if err != nil {
			server.WriteError(w, http.StatusInternalServerError, "failed to get CORS origins")
			return
		}
		origins = val
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"cors_origins": origins,
		"env_override": envOverride,
	})
}

func (h *handler) updateCORSOrigins(w http.ResponseWriter, r *http.Request) {
	if h.cfg != nil && len(h.cfg.CORSAllowedOrigins) > 0 {
		server.WriteError(w, http.StatusConflict, "CORS origins are set via OPENTRACE_CORS_ORIGINS environment variable and cannot be changed from the UI")
		return
	}

	var req struct {
		CORSOrigins string `json:"cors_origins"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if h.settingsStore == nil {
		server.WriteError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}

	if err := h.settingsStore.SetCORSOrigins(r.Context(), req.CORSOrigins); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to save CORS origins")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]string{"cors_origins": req.CORSOrigins})
}

func (h *handler) getQueryGuardrails(w http.ResponseWriter, r *http.Request) {
	envOverride := h.cfg != nil && (os.Getenv("OPENTRACE_MAX_QUERY_ROWS") != "" || os.Getenv("OPENTRACE_STATEMENT_TIMEOUT_MS") != "")

	maxRows := 500
	stmtTimeout := 5000

	if envOverride {
		if h.cfg != nil {
			maxRows = h.cfg.MaxQueryRows
			stmtTimeout = h.cfg.StatementTimeoutMS
		}
	} else if h.settingsStore != nil {
		if v, err := h.settingsStore.GetMaxQueryRows(r.Context()); err == nil && v > 0 {
			maxRows = v
		}
		if v, err := h.settingsStore.GetStatementTimeout(r.Context()); err == nil && v > 0 {
			stmtTimeout = v
		}
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"max_query_rows":       maxRows,
		"statement_timeout_ms": stmtTimeout,
		"env_override":         envOverride,
	})
}

func (h *handler) updateQueryGuardrails(w http.ResponseWriter, r *http.Request) {
	if h.cfg != nil && (os.Getenv("OPENTRACE_MAX_QUERY_ROWS") != "" || os.Getenv("OPENTRACE_STATEMENT_TIMEOUT_MS") != "") {
		server.WriteError(w, http.StatusConflict, "Query guardrails are set via environment variables and cannot be changed from the UI")
		return
	}

	var req struct {
		MaxQueryRows       int `json:"max_query_rows"`
		StatementTimeoutMS int `json:"statement_timeout_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.MaxQueryRows < 1 {
		server.WriteError(w, http.StatusBadRequest, "max_query_rows must be >= 1")
		return
	}
	if req.StatementTimeoutMS < 100 {
		server.WriteError(w, http.StatusBadRequest, "statement_timeout_ms must be >= 100")
		return
	}

	if h.settingsStore == nil {
		server.WriteError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}

	if err := h.settingsStore.SetMaxQueryRows(r.Context(), req.MaxQueryRows); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to save max_query_rows")
		return
	}
	if err := h.settingsStore.SetStatementTimeout(r.Context(), req.StatementTimeoutMS); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to save statement_timeout_ms")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"max_query_rows":       req.MaxQueryRows,
		"statement_timeout_ms": req.StatementTimeoutMS,
	})
}

func (h *handler) getMCPName(w http.ResponseWriter, r *http.Request) {
	envOverride := os.Getenv("OPENTRACE_MCP_NAME") != ""

	name := "opentrace"
	if envOverride {
		name = os.Getenv("OPENTRACE_MCP_NAME")
	} else if h.settingsStore != nil {
		if v, err := h.settingsStore.GetMCPName(r.Context()); err == nil && v != "" {
			name = v
		}
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"mcp_name":     name,
		"env_override": envOverride,
	})
}

func (h *handler) updateMCPName(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("OPENTRACE_MCP_NAME") != "" {
		server.WriteError(w, http.StatusConflict, "MCP name is set via OPENTRACE_MCP_NAME environment variable and cannot be changed from the UI")
		return
	}

	var req struct {
		MCPName string `json:"mcp_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if h.settingsStore == nil {
		server.WriteError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}

	if err := h.settingsStore.SetMCPName(r.Context(), req.MCPName); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to save MCP name")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]string{"mcp_name": req.MCPName})
}

func (h *handler) getSamplingRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.settingsStore.GetSamplingRules(r.Context())
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to get sampling rules")
		return
	}
	if rules == nil {
		rules = []store.SamplingRule{}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"sampling_rules": rules})
}

func (h *handler) updateSamplingRules(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rules []store.SamplingRule `json:"sampling_rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Validate
	for i, rule := range body.Rules {
		if rule.Rate < 0 || rule.Rate > 1 {
			server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("rule %d: rate must be between 0 and 1", i))
			return
		}
	}
	if err := h.settingsStore.SetSamplingRules(r.Context(), body.Rules); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to save sampling rules")
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"sampling_rules": body.Rules})
}

func (h *handler) auditLog(w http.ResponseWriter, r *http.Request) {
	entries, err := h.auditStore.Recent(r.Context(), 100)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to fetch audit log")
		return
	}
	server.WriteJSON(w, http.StatusOK, entries)
}

// ── Page handlers ────────────────────────────────────────────

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

// settingsPage renders the settings page.
func (h *handler) settingsPage(w http.ResponseWriter, r *http.Request) {
	layout := h.layoutData(r, "Settings", "settings")

	sd := webviews.SettingsData{
		CSRFToken:          server.CSRFToken(r.Context()),
		RetentionDays:      30,
		MaxQueryRows:       500,
		StatementTimeoutMS: 5000,
		MCPName:            "opentrace",
	}

	if h.settingsStore != nil {
		settings, err := h.settingsStore.GetRetention(r.Context())
		if err == nil {
			sd.RetentionDays = settings.RetentionDays
			sd.MetricRetentionDays = settings.MetricRetentionDays
		}
	}

	sd.EnvKeyOverride = h.cfg != nil && h.cfg.APIKey != ""
	sd.CORSEnvOverride = h.cfg != nil && len(h.cfg.CORSAllowedOrigins) > 0
	if sd.CORSEnvOverride {
		sd.CORSOrigins = strings.Join(h.cfg.CORSAllowedOrigins, ",")
	} else if h.settingsStore != nil {
		if val, err := h.settingsStore.GetCORSOrigins(r.Context()); err == nil {
			sd.CORSOrigins = val
		}
	}

	// Query guardrails
	sd.QueryEnvOverride = os.Getenv("OPENTRACE_MAX_QUERY_ROWS") != "" || os.Getenv("OPENTRACE_STATEMENT_TIMEOUT_MS") != ""
	if sd.QueryEnvOverride && h.cfg != nil {
		sd.MaxQueryRows = h.cfg.MaxQueryRows
		sd.StatementTimeoutMS = h.cfg.StatementTimeoutMS
	} else if h.settingsStore != nil {
		if v, err := h.settingsStore.GetMaxQueryRows(r.Context()); err == nil && v > 0 {
			sd.MaxQueryRows = v
		}
		if v, err := h.settingsStore.GetStatementTimeout(r.Context()); err == nil && v > 0 {
			sd.StatementTimeoutMS = v
		}
	}

	// MCP name
	sd.MCPNameEnvOverride = os.Getenv("OPENTRACE_MCP_NAME") != ""
	if sd.MCPNameEnvOverride {
		sd.MCPName = os.Getenv("OPENTRACE_MCP_NAME")
	} else if h.settingsStore != nil {
		if v, err := h.settingsStore.GetMCPName(r.Context()); err == nil && v != "" {
			sd.MCPName = v
		}
	}

	webviews.SettingsPage(layout, sd).Render(r.Context(), w)
}

// setupPage renders the setup/onboarding page.
func (h *handler) setupPage(w http.ResponseWriter, r *http.Request) {
	layout := h.layoutData(r, "Setup", "setup")
	setup := webviews.SetupData{
		APIKey: h.effectiveAPIKey(r.Context()),
	}
	webviews.SetupPage(layout, setup).Render(r.Context(), w)
}

// usersPage renders the user management page.
func (h *handler) usersPage(w http.ResponseWriter, r *http.Request) {
	layout := h.layoutData(r, "Users", "users")
	var users []store.User
	if h.userStore != nil {
		u, err := h.userStore.List(r.Context())
		if err == nil {
			users = u
		}
	}
	webviews.UsersPage(layout, users, layout.IsAdmin).Render(r.Context(), w)
}
