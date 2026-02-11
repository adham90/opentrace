package web

import (
	"encoding/json"
	"net/http"

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

func (s *Server) handleGetAutoUpdate(w http.ResponseWriter, r *http.Request) {
	if s.settingsStore == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": false})
		return
	}
	enabled, err := s.settingsStore.GetAutoUpdate(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get auto-update setting")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

func (s *Server) handleSetAutoUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if s.settingsStore == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	if err := s.settingsStore.SetAutoUpdate(r.Context(), req.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save auto-update setting")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": req.Enabled})
}

func (s *Server) handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	if isDocker() {
		writeError(w, http.StatusConflict, "self-update is not available in Docker; use docker compose pull")
		return
	}

	result, err := s.selfUpdater.Update(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)

	// Schedule a restart after the response is sent.
	go func() {
		// Small delay so the HTTP response gets flushed to the client.
		select {
		case <-s.restartCh:
			// Already closing
		default:
			close(s.restartCh)
		}
	}()
}
