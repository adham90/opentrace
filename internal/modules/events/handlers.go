package events

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
)

type handler struct {
	eventStore           store.EventStore
	codeEntityStore      store.CodeEntityStore
	testCorrelationStore store.TestCorrelationStore
}

func (h *handler) webhook(w http.ResponseWriter, r *http.Request) {
	if h.eventStore == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "event tracking not available")
		return
	}

	eventType := store.EventType(chi.URLParam(r, "type"))
	switch eventType {
	case store.EventTypePR, store.EventTypeTest, store.EventTypeAlert,
		store.EventTypeCommit, store.EventTypeCustom, store.EventTypeDeploy:
		// valid
	default:
		server.WriteError(w, http.StatusBadRequest, "invalid event type: must be pr, test, alert, commit, deploy, or custom")
		return
	}

	var body struct {
		Source      string         `json:"source"`
		Service    string         `json:"service"`
		Title      string         `json:"title"`
		Description string        `json:"description"`
		Metadata   map[string]any `json:"metadata"`
		ExternalID  string        `json:"external_id"`
		ExternalURL string        `json:"external_url"`
		Author     string         `json:"author"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if body.Title == "" {
		server.WriteError(w, http.StatusBadRequest, "title is required")
		return
	}

	e, err := h.eventStore.Create(r.Context(), store.CreateEventParams{
		EventType:   eventType,
		Source:      body.Source,
		Service:    body.Service,
		Title:      body.Title,
		Description: body.Description,
		Metadata:   body.Metadata,
		ExternalID:  body.ExternalID,
		ExternalURL: body.ExternalURL,
		Author:     body.Author,
	})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to create event")
		return
	}

	server.WriteJSON(w, http.StatusCreated, e)
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	if h.eventStore == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "event tracking not available")
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

	events, err := h.eventStore.List(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list events")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"count":  len(events),
		"events": events,
	})
}

func (h *handler) listCodeEntities(w http.ResponseWriter, r *http.Request) {
	if h.codeEntityStore == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "code entity tracking not available")
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

	entities, err := h.codeEntityStore.TopByRisk(r.Context(), service, limit)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list code entities")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"count":    len(entities),
		"entities": entities,
	})
}

func (h *handler) listTestGaps(w http.ResponseWriter, r *http.Request) {
	if h.testCorrelationStore == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "test correlation not available")
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

	paths, err := h.testCorrelationStore.TopByPriority(r.Context(), service, limit)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list test gaps")
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"count": len(paths),
		"paths": paths,
	})
}
