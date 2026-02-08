package agent

import (
	"strings"
	"testing"
)

func TestTruncate_Short(t *testing.T) {
	s := "hello"
	got := TruncateObservation(s, 100)
	if got != s {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestTruncate_Exact(t *testing.T) {
	s := "hello"
	got := TruncateObservation(s, len(s))
	if got != s {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestTruncate_Long(t *testing.T) {
	s := strings.Repeat("x", 200)
	got := TruncateObservation(s, 100)
	if len(got) > 100+len("\n[truncated]") {
		t.Errorf("expected truncated, got length %d", len(got))
	}
	if !strings.HasSuffix(got, "\n[truncated]") {
		t.Errorf("expected truncated marker, got %q", got[len(got)-20:])
	}
}

func TestTruncate_Empty(t *testing.T) {
	got := TruncateObservation("", 100)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
