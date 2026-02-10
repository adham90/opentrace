package web

import (
	"net/http"
	"strconv"
)

func (s *Server) handleDigestsPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Digests", "digests")
	tmpl := s.getTemplate(digestsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/digests.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleGetLatestDigest(w http.ResponseWriter, r *http.Request) {
	if s.digestStore == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "unavailable", "message": "digest store not configured"})
		return
	}

	env := r.URL.Query().Get("env")
	d, err := s.digestStore.GetLatest(r.Context(), env)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "empty", "message": "no digests yet"})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleListDigests(w http.ResponseWriter, r *http.Request) {
	if s.digestStore == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	env := r.URL.Query().Get("env")
	limit := 30
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	digests, err := s.digestStore.List(r.Context(), limit, env)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list digests")
		return
	}
	if digests == nil {
		digests = nil
	}
	writeJSON(w, http.StatusOK, digests)
}
