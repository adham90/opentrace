package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/opentrace/opentrace/internal/store"
)

func (s *Server) handleListChats(w http.ResponseWriter, r *http.Request) {
	if s.chatStore == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	chats, err := s.chatStore.ListChats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if chats == nil {
		chats = []store.Chat{}
	}
	writeJSON(w, http.StatusOK, chats)
}

func (s *Server) handleGetChat(w http.ResponseWriter, r *http.Request) {
	if s.chatStore == nil {
		writeError(w, http.StatusNotFound, "chat store not configured")
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat ID")
		return
	}

	chat, err := s.chatStore.GetChat(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "chat not found")
		return
	}

	msgs, err := s.chatStore.GetMessages(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if msgs == nil {
		msgs = []store.Message{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"chat":     chat,
		"messages": msgs,
	})
}

func (s *Server) handleDeleteChat(w http.ResponseWriter, r *http.Request) {
	if s.chatStore == nil {
		writeError(w, http.StatusNotFound, "chat store not configured")
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat ID")
		return
	}

	if err := s.chatStore.DeleteChat(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "chat not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
