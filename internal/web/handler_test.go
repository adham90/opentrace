package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/opentrace/opentrace/internal/connector"
)

func TestHealthCheck(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil, connector.NewRegistry(), nil, nil, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want %q", body["status"], "ok")
	}
}

func TestNotFound(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil, connector.NewRegistry(), nil, nil, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestChatPage_ValidChat(t *testing.T) {
	cs := newMockChatStore()
	chat, _ := cs.CreateChat(nil, "Test Chat")

	srv := NewServer(nil, nil, nil, cs, nil, connector.NewRegistry(), nil, nil, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/chats/"+chat.ID.String(), nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, chat.ID.String()) {
		t.Errorf("body does not contain chat ID %s", chat.ID.String())
	}
}

func TestChatPage_InvalidUUID(t *testing.T) {
	cs := newMockChatStore()
	srv := NewServer(nil, nil, nil, cs, nil, connector.NewRegistry(), nil, nil, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/chats/not-a-uuid", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	loc := w.Header().Get("Location")
	if loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}
}

func TestChatPage_NotFound(t *testing.T) {
	cs := newMockChatStore()
	srv := NewServer(nil, nil, nil, cs, nil, connector.NewRegistry(), nil, nil, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/chats/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	loc := w.Header().Get("Location")
	if loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}
}

