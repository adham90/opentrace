package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/adham90/opentrace/internal/store"
)

func (s *Server) handleGetRetention(w http.ResponseWriter, r *http.Request) {
	settings, err := s.settingsStore.GetRetention(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get retention settings")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUpdateRetention(w http.ResponseWriter, r *http.Request) {
	var req store.RetentionSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.RetentionDays < 0 {
		writeError(w, http.StatusBadRequest, "retention_days must be >= 0")
		return
	}
	if err := s.settingsStore.SetRetention(r.Context(), req); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save retention settings")
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleGetAPIKey(w http.ResponseWriter, r *http.Request) {
	envOverride := s.cfg != nil && s.cfg.APIKey != ""
	apiKey := s.getEffectiveAPIKey(r.Context())

	writeJSON(w, http.StatusOK, map[string]any{
		"api_key":      apiKey,
		"env_override": envOverride,
	})
}

func (s *Server) handleRegenerateAPIKey(w http.ResponseWriter, r *http.Request) {
	// Block regeneration if env var override is active
	if s.cfg != nil && s.cfg.APIKey != "" {
		writeError(w, http.StatusConflict, "API key is set via OPENTRACE_API_KEY environment variable and cannot be regenerated from the UI")
		return
	}

	key, err := generateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate API key")
		return
	}

	if s.settingsStore == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}

	if err := s.settingsStore.SetAPIKey(r.Context(), key); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store API key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"api_key": key})
}

func (s *Server) handleGetCORSOrigins(w http.ResponseWriter, r *http.Request) {
	envOverride := s.cfg != nil && len(s.cfg.CORSAllowedOrigins) > 0

	var origins string
	if envOverride {
		origins = strings.Join(s.cfg.CORSAllowedOrigins, ",")
	} else if s.settingsStore != nil {
		val, err := s.settingsStore.GetCORSOrigins(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get CORS origins")
			return
		}
		origins = val
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"cors_origins": origins,
		"env_override": envOverride,
	})
}

func (s *Server) handleUpdateCORSOrigins(w http.ResponseWriter, r *http.Request) {
	if s.cfg != nil && len(s.cfg.CORSAllowedOrigins) > 0 {
		writeError(w, http.StatusConflict, "CORS origins are set via OPENTRACE_CORS_ORIGINS environment variable and cannot be changed from the UI")
		return
	}

	var req struct {
		CORSOrigins string `json:"cors_origins"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if s.settingsStore == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}

	if err := s.settingsStore.SetCORSOrigins(r.Context(), req.CORSOrigins); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save CORS origins")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"cors_origins": req.CORSOrigins})
}

// maskAPIKey returns a masked version of an API key for safe display.
// Shows the first 8 characters followed by "****", or empty if key is empty.
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return key[:1] + "****"
	}
	return key[:8] + "****"
}

// isMaskedValue returns true if the value looks like a masked API key
// that was returned by the GET endpoint (user didn't change it).
func isMaskedValue(val string) bool {
	return strings.HasSuffix(val, "****")
}

