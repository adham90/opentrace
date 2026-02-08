package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opentrace/opentrace/internal/connector"
)

func TestSSEEndpoint_Headers(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil, connector.NewRegistry(), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sse/demo", nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}

	cc := w.Header().Get("Cache-Control")
	if cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-cache")
	}

	conn := w.Header().Get("Connection")
	if conn != "keep-alive" {
		t.Errorf("Connection = %q, want %q", conn, "keep-alive")
	}
}

func TestSSEEndpoint_EventFormat(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil, connector.NewRegistry(), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sse/demo", nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	scanner := bufio.NewScanner(w.Body)
	var events []map[string]any

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var event map[string]any
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				t.Fatalf("invalid JSON in SSE data: %v, line: %q", err, data)
			}
			if _, ok := event["step_type"]; !ok {
				t.Errorf("event missing step_type field: %v", event)
			}
			events = append(events, event)
		}
	}

	if len(events) == 0 {
		t.Fatal("no events received")
	}
}

func TestSSEEndpoint_EventSequence(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil, connector.NewRegistry(), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sse/demo", nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	scanner := bufio.NewScanner(w.Body)
	var stepTypes []string

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var event map[string]any
			json.Unmarshal([]byte(data), &event)
			if st, ok := event["step_type"].(string); ok {
				stepTypes = append(stepTypes, st)
			}
		}
	}

	expected := []string{"thinking", "tool_call", "observation", "final"}
	if len(stepTypes) != len(expected) {
		t.Fatalf("step types = %v, want %v", stepTypes, expected)
	}
	for i, want := range expected {
		if stepTypes[i] != want {
			t.Errorf("step[%d] = %q, want %q", i, stepTypes[i], want)
		}
	}
}

func TestSSEEndpoint_ClientDisconnect(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil, connector.NewRegistry(), nil, nil)

	// Create a context that we cancel immediately
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/sse/demo", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	// Should not panic
	srv.Router.ServeHTTP(w, req)
}
