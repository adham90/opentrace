package apiclient

import (
	"strings"
	"testing"
)

func TestLogStreamURL_BuildsQuery(t *testing.T) {
	c := New("http://localhost:8080", "")

	got, err := c.LogStreamURL("error", "api", "boom", "production")
	if err != nil {
		t.Fatalf("LogStreamURL: %v", err)
	}
	if !strings.HasPrefix(got, "http://localhost:8080/api/cli/logs/stream?") {
		t.Fatalf("unexpected URL: %s", got)
	}
	for _, want := range []string{"level=error", "service=api", "search=boom", "env=production"} {
		if !strings.Contains(got, want) {
			t.Errorf("URL %s missing %s", got, want)
		}
	}

	bare, err := c.LogStreamURL("", "", "", "")
	if err != nil {
		t.Fatalf("LogStreamURL: %v", err)
	}
	if bare != "http://localhost:8080/api/cli/logs/stream" {
		t.Errorf("bare URL = %q", bare)
	}
}

// TestLogStreamURL_InvalidEndpoint covers the nil-*url.URL dereference: a
// malformed endpoint (it comes from user-editable config) must produce an
// error, not a panic.
func TestLogStreamURL_InvalidEndpoint(t *testing.T) {
	c := New("http://local host:8080", "")

	got, err := c.LogStreamURL("", "", "", "")
	if err == nil {
		t.Fatalf("expected an error for a malformed endpoint, got %q", got)
	}
	if !strings.Contains(err.Error(), "invalid endpoint") {
		t.Errorf("error = %v, want it to mention the invalid endpoint", err)
	}
}
