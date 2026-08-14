package engine

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/index"
	"github.com/adham90/opentrace/internal/logstore/wal"
)

// queryView returns the two halves of the searchable world for a time range,
// snapshotted together: the registered sealed segments overlapping the range,
// and the WAL files that hold everything not yet in a segment (the live WAL
// plus any rotated file whose seal has not completed).
//
// Both halves are taken under pendingMu, which seal registration also holds, so
// a rotated hour is never missing from both lists (the old "seal blackout") nor
// present in both (double counting).
func (s *Store) queryView(start, end time.Time) ([]*LoadedSegment, []string) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	return s.segments.SegmentsInRange(start, end), s.walPathsLocked()
}

// walPaths returns every WAL file a query must scan. Caller must not hold
// pendingMu.
func (s *Store) walPaths() []string {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	return s.walPathsLocked()
}

func (s *Store) walPathsLocked() []string {
	paths := make([]string, 0, len(s.pendingSeals)+1)
	paths = append(paths, s.writer.WALPath())
	for _, p := range s.pendingSeals {
		paths = append(paths, p)
	}
	return paths
}

// readWALEntries reads one WAL file, tolerating a torn trailing record: the
// fully-parsed prefix is returned and the tear is logged. Discarding the whole
// file on a torn tail would hide an entire hour of live logs.
func readWALEntries(path string) []chunk.Entry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	entries, err := wal.ReadEntries(f)
	if err != nil {
		slog.Warn("query: WAL had a torn tail; using parsed entries",
			"error", err, "wal", filepath.Base(path), "parsed", len(entries))
	}
	return entries
}

// isRequestEntry reports whether an entry describes an HTTP request. The label
// can arrive as kind="request" (current SDKs), event_type="http.request" (the
// adapter's own mapping), or as a timed row carrying request identity (older
// rows and SDKs that set their own event_type).
func isRequestEntry(e *chunk.Entry) bool {
	if strings.EqualFold(e.Kind, "request") || strings.EqualFold(e.EventType, "http.request") {
		return true
	}
	return e.DurationMs > 0 && (e.Method != "" || e.Path != "" || e.Handler != "")
}

// handlerMatches reports whether a stored handler satisfies a controller-style
// filter. Handlers are stored framework-agnostically as "Controller#action",
// but callers (the MCP performance tool, the UI) filter by the bare controller
// name, which an equality test could never match.
func handlerMatches(stored, want string) bool {
	if strings.EqualFold(stored, want) {
		return true
	}
	if i := strings.Index(stored, "#"); i >= 0 {
		return strings.EqualFold(stored[:i], want)
	}
	return false
}

// messageMatchesQuery applies the inverted index's semantics (lowercased tokens,
// all tokens must be present, order-independent) to a raw message, so unsealed
// rows answer a full-text query the same way sealed chunks do.
func messageMatchesQuery(message, query string) bool {
	tokens := index.TokenizeUnique(query)
	if len(tokens) == 0 {
		// The index yields no postings for such a query and matches nothing;
		// match nothing here too rather than falling back to substring.
		return false
	}
	have := make(map[string]struct{})
	for _, t := range index.Tokenize(message) {
		have[t] = struct{}{}
	}
	for _, t := range tokens {
		if _, ok := have[t]; !ok {
			return false
		}
	}
	return true
}

// matchesMetadata reports whether the entry's body metadata satisfies every
// key/value pair in filter. The filter reaches the engine from the MCP
// metadata_filter argument; it used to be dropped silently, so a narrow query
// came back with every row and read as the answer.
func matchesMetadata(e *chunk.Entry, filter map[string]string) bool {
	if len(filter) == 0 {
		return true
	}
	meta := entryMetadata(e)
	if meta == nil {
		return false
	}
	for k, want := range filter {
		got, ok := meta[k]
		if !ok || fmt.Sprintf("%v", got) != want {
			return false
		}
	}
	return true
}

// entryMetadata decodes the "metadata" object from an entry's body blob.
func entryMetadata(e *chunk.Entry) map[string]any {
	if len(e.Body) == 0 {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(e.Body, &body); err != nil {
		return nil
	}
	if meta, ok := body["metadata"].(map[string]any); ok {
		return meta
	}
	return nil
}

// matchesExclude rejects entries whose field value appears in a comma-separated
// exclusion list (field -> "a,b,c").
func matchesExclude(e *chunk.Entry, exclude map[string]string) bool {
	for field, list := range exclude {
		value := excludableField(e, field)
		if value == "" {
			continue
		}
		for _, v := range strings.Split(list, ",") {
			if v = strings.TrimSpace(v); v != "" && strings.EqualFold(v, value) {
				return false
			}
		}
	}
	return true
}

// excludableField resolves the fields an Exclude filter may name. Unknown
// fields return "" and are ignored (they can't be honoured, and inventing a
// match would drop rows the caller never asked to exclude).
func excludableField(e *chunk.Entry, field string) string {
	if v := dictColumnValue(e, field); v != "" {
		return v
	}
	v, _ := scanColumnValue(e, field)
	return v
}

// forEachWALEntry calls fn for every entry in every given WAL file. fn returns
// false to stop the scan early.
func forEachWALEntry(paths []string, fn func(e *chunk.Entry) bool) {
	for _, path := range paths {
		entries := readWALEntries(path)
		for i := range entries {
			if !fn(&entries[i]) {
				return
			}
		}
	}
}
