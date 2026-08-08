package engine

import (
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

// SourceFile, CommitHash and SinceID were part of the search params but had no
// clause in matchesParams, so they were accepted and ignored. An ignored filter
// is worse than a rejected one: the caller gets every row back and reads it as
// the answer to the narrower question it asked.
func TestMatchesParams_HonoursPreviouslyIgnoredFilters(t *testing.T) {
	entry := &chunk.Entry{
		ID:         100,
		Ts:         time.Now().UnixMilli(),
		Level:      "error",
		Service:    "api",
		Message:    "boom",
		SourceFile: "cmd/api/fleet.go",
		Version:    "abc123",
	}

	tests := []struct {
		name  string
		p     SearchParams
		match bool
	}{
		{"source file matches", SearchParams{SourceFile: "cmd/api/fleet.go"}, true},
		{"source file differs", SearchParams{SourceFile: "cmd/api/chat.go"}, false},
		{"commit matches (stored as Version)", SearchParams{CommitHash: "abc123"}, true},
		{"commit differs", SearchParams{CommitHash: "def456"}, false},
		{"cursor before entry", SearchParams{SinceID: 99}, true},
		{"cursor at entry excludes it", SearchParams{SinceID: 100}, false},
		{"cursor past entry", SearchParams{SinceID: 101}, false},
		{"no filters", SearchParams{}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesParams(entry, tc.p); got != tc.match {
				t.Errorf("matchesParams = %v, want %v", got, tc.match)
			}
		})
	}
}

// The tail cursor contract: passing back the last id you saw must not hand you
// that same row again, or a poller re-reports the same lines forever.
func TestMatchesParams_CursorIsExclusive(t *testing.T) {
	seen := int64(42)
	at := func(id int64) bool {
		return matchesParams(&chunk.Entry{ID: id, Ts: time.Now().UnixMilli()}, SearchParams{SinceID: seen})
	}
	if at(seen) {
		t.Error("the row at the cursor was returned again")
	}
	if !at(seen + 1) {
		t.Error("the row after the cursor was skipped")
	}
}
