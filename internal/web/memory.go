package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/opentrace/opentrace/internal/store"
)

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	if s.memoryStore == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	category := r.URL.Query().Get("category")
	memories, err := s.memoryStore.ListMemories(r.Context(), category)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if memories == nil {
		memories = []store.MemoryEntry{}
	}
	writeJSON(w, http.StatusOK, memories)
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	if s.memoryStore == nil {
		writeError(w, http.StatusNotFound, "memory store not configured")
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid memory ID")
		return
	}

	if err := s.memoryStore.DeleteMemory(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "memory not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
