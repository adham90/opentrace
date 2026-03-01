package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/store"
)

// handleEventWebhook accepts generic events from CI/CD pipelines and integrations.
// POST /api/events/{type}
func (s *Server) handleEventWebhook(w http.ResponseWriter, r *http.Request) {
	if s.eventStore == nil {
		writeError(w, http.StatusServiceUnavailable, "event tracking not available")
		return
	}

	eventType := store.EventType(chi.URLParam(r, "type"))
	switch eventType {
	case store.EventTypePR, store.EventTypeTest, store.EventTypeAlert,
		store.EventTypeCommit, store.EventTypeCustom:
		// valid
	default:
		writeError(w, http.StatusBadRequest, "invalid event type: must be pr, test, alert, commit, or custom")
		return
	}

	var body struct {
		Source      string         `json:"source"`
		Service     string         `json:"service"`
		Title       string         `json:"title"`
		Description string         `json:"description"`
		Metadata    map[string]any `json:"metadata"`
		ExternalID  string         `json:"external_id"`
		ExternalURL string         `json:"external_url"`
		Author      string         `json:"author"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if body.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	e, err := s.eventStore.Create(r.Context(), store.CreateEventParams{
		EventType:   eventType,
		Source:      body.Source,
		Service:     body.Service,
		Title:       body.Title,
		Description: body.Description,
		Metadata:    body.Metadata,
		ExternalID:  body.ExternalID,
		ExternalURL: body.ExternalURL,
		Author:      body.Author,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create event")
		return
	}

	writeJSON(w, http.StatusCreated, e)
}

// handleListEvents returns recent events.
// GET /api/events?type=...&service=...&limit=...
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	if s.eventStore == nil {
		writeError(w, http.StatusServiceUnavailable, "event tracking not available")
		return
	}

	params := store.ListEventParams{}
	if t := r.URL.Query().Get("type"); t != "" {
		params.EventType = store.EventType(t)
	}
	if svc := r.URL.Query().Get("service"); svc != "" {
		params.Service = svc
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			params.Limit = n
		}
	}

	events, err := s.eventStore.List(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list events")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"count":  len(events),
		"events": events,
	})
}

// handleListCodeEntities returns code entities.
// GET /api/code-entities?service=...&limit=...
func (s *Server) handleListCodeEntities(w http.ResponseWriter, r *http.Request) {
	if s.codeEntityStore == nil {
		writeError(w, http.StatusServiceUnavailable, "code entity tracking not available")
		return
	}

	service := r.URL.Query().Get("service")
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}

	entities, err := s.codeEntityStore.TopByRisk(r.Context(), service, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list code entities")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"count":    len(entities),
		"entities": entities,
	})
}

// handleListTestGaps returns uncovered error paths.
// GET /api/test-gaps?service=...&limit=...
func (s *Server) handleListTestGaps(w http.ResponseWriter, r *http.Request) {
	if s.testCorrelationStore == nil {
		writeError(w, http.StatusServiceUnavailable, "test correlation not available")
		return
	}

	service := r.URL.Query().Get("service")
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}

	paths, err := s.testCorrelationStore.TopByPriority(r.Context(), service, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list test gaps")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(paths),
		"paths": paths,
	})
}
