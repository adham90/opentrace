package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/store"
)

func (s *Server) handleListErrorGroups(w http.ResponseWriter, r *http.Request) {
	params := store.ListErrorGroupParams{
		Status:      store.ErrorGroupStatus(r.URL.Query().Get("status")),
		Service:     r.URL.Query().Get("service"),
		Environment: r.URL.Query().Get("environment"),
		SortBy:      r.URL.Query().Get("sort_by"),
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			params.Limit = v
		}
	}

	groups, err := s.errorGroupStore.List(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list error groups")
		return
	}
	if groups == nil {
		groups = []store.ErrorGroup{}
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) handleGetErrorGroup(w http.ResponseWriter, r *http.Request) {
	fp := chi.URLParam(r, "fingerprint")
	eg, err := s.errorGroupStore.Get(r.Context(), fp)
	if err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "error group not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get error group")
		return
	}

	events, _ := s.errorGroupStore.ListEvents(r.Context(), fp, 10)
	eg.Events = events

	writeJSON(w, http.StatusOK, eg)
}

func (s *Server) handleResolveErrorGroup(w http.ResponseWriter, r *http.Request) {
	fp := chi.URLParam(r, "fingerprint")

	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := s.errorGroupStore.Resolve(r.Context(), fp, body.Reason); err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "error group not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve error group")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}

func (s *Server) handleIgnoreErrorGroup(w http.ResponseWriter, r *http.Request) {
	fp := chi.URLParam(r, "fingerprint")

	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := s.errorGroupStore.Ignore(r.Context(), fp, body.Reason); err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "error group not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to ignore error group")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
}
