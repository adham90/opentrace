package events

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

type handler struct {
	eventStore store.EventStore
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
		Service     string         `json:"service"`
		Title       string         `json:"title"`
		Description string         `json:"description"`
		Metadata    map[string]any `json:"metadata"`
		ExternalID  string         `json:"external_id"`
		ExternalURL string         `json:"external_url"`
		Author      string         `json:"author"`
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
		Service:     body.Service,
		Title:       body.Title,
		Description: body.Description,
		Metadata:    body.Metadata,
		ExternalID:  body.ExternalID,
		ExternalURL: body.ExternalURL,
		Author:      body.Author,
	})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to create event")
		return
	}

	server.WriteJSON(w, http.StatusCreated, e)
}
