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
