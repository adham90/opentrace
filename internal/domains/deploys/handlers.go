package deploys

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

type handler struct {
	store           store.DeployStore
	onDeployCreated func(ctx context.Context, d store.Deploy)
}

func (h *handler) webhook(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "deploy tracking not available")
		return
	}

	var body struct {
		Service      string   `json:"service"`
		Environment  string   `json:"environment"`
		CommitHash   string   `json:"commit_hash"`
		Branch       string   `json:"branch"`
		Author       string   `json:"author"`
		FilesChanged []string `json:"files_changed"`
		DeployedAt   string   `json:"deployed_at"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if body.CommitHash == "" {
		server.WriteError(w, http.StatusBadRequest, "commit_hash is required")
		return
	}

	params := store.CreateDeployParams{
		Service:      body.Service,
		Environment:  body.Environment,
		CommitHash:   body.CommitHash,
		Branch:       body.Branch,
		Author:       body.Author,
		FilesChanged: body.FilesChanged,
		DeploySource: store.DeploySourceWebhook,
	}

	if body.DeployedAt != "" {
		if t, err := time.Parse(time.RFC3339, body.DeployedAt); err == nil {
			params.DeployedAt = &t
		}
	}

	d, err := h.store.Create(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to record deploy")
		return
	}

	// Notify deploy watcher for post-deploy observation
	if h.onDeployCreated != nil {
		go h.onDeployCreated(r.Context(), *d)
	}

	server.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":          d.ID,
		"service":     d.Service,
		"commit_hash": d.CommitHash,
		"status":      d.Status,
		"deployed_at": d.DeployedAt.Format(time.RFC3339),
	})
}
