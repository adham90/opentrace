package ingest

import (
	"strings"
	"unicode/utf8"
)

// Field-length caps applied at the ingest boundary.
//
// The columnar WAL encodes every string field with a 16-bit length prefix, so a
// single field larger than 65535 bytes mis-frames the segment and corrupts every
// entry after it. Log payloads are fully attacker/SDK controlled, so ingest —
// the outermost boundary — is where absurd values get cut down. The caps below
// are deliberately far under the encoder limit so that even the truncation
// marker can never push a field over it.
const (
	// maxIdentFieldBytes caps short identifier-shaped fields (service, env,
	// host, kind, trace/span/request/user/tenant/session IDs, method, job
	// class/queue/id, error class). Real values are tens of bytes.
	maxIdentFieldBytes = 512

	// maxPathFieldBytes caps path-shaped fields (path, route, handler,
	// source file). URLs can be long but not unbounded.
	maxPathFieldBytes = 2048

	// maxMessageBytes caps human-readable text (message, error_message).
	maxMessageBytes = 16384

	// truncationMarker is appended to any field that had to be cut so the
	// truncation is visible to whoever reads the log instead of silent.
	truncationMarker = "…[truncated]"
)

// truncateField returns s clipped to at most max bytes, cutting on a UTF-8 rune
// boundary and appending truncationMarker when anything was removed. Values at
// or under the cap are returned unchanged (the hot path allocates nothing).
func truncateField(s string, max int) string {
	if len(s) <= max {
		return s
	}
	keep := max - len(truncationMarker)
	if keep < 0 {
		keep = 0
	}
	// Back up to a rune boundary so we never emit invalid UTF-8.
	for keep > 0 && !utf8.RuneStart(s[keep]) {
		keep--
	}
	return s[:keep] + truncationMarker
}

// validLevels is the set of levels both ingest endpoints accept. "warning" is
// accepted on the wire and canonicalised to "warn" by normalizeLevel.
var validLevels = map[string]bool{
	"debug": true, "info": true, "warn": true, "warning": true,
	"error": true, "fatal": true,
}

// validLevelList is the human-readable form used in 400 responses.
const validLevelList = "debug, info, warn, error, fatal"

// isValidLevel reports whether the (already lowercased) level is accepted.
func isValidLevel(level string) bool { return validLevels[level] }

// Entry kinds. `kind` is the SDK's own discriminator for what a row represents;
// it is the only reliable signal for "is this an HTTP request?".
const (
	kindLog     = "log"
	kindRequest = "request"
	kindJob     = "job"
	kindEvent   = "event"
)

// httpRequestEventType is the canonical event_type for HTTP request rows.
const httpRequestEventType = "http.request"

// deriveKind resolves the entry kind. An explicit SDK-provided kind always
// wins; otherwise it is inferred from the fields actually present.
//
// Note what is NOT a signal here: duration_ms. Background jobs and generic
// timed events carry durations too, and classifying every timed row as an HTTP
// request made the storage layer stamp event_type=http.request over the SDK's
// own value, hiding jobs from job views and polluting endpoint latency.
func deriveKind(fe flatEntry) string {
	if k := strings.ToLower(strings.TrimSpace(fe.Kind)); k != "" {
		return k
	}
	if fe.EventType == httpRequestEventType {
		return kindRequest
	}
	// HTTP evidence outranks job fields: a request handled inside a job context
	// still carries method/path/status, and classifying it as a job stripped
	// its RequestSummary and removed it from every request view.
	if fe.Method != "" || fe.Path != "" || fe.Route != "" || fe.Status > 0 {
		return kindRequest
	}
	if fe.JobClass != "" || fe.JobQueue != "" || fe.JobID != "" {
		return kindJob
	}
	if fe.EventType != "" {
		return kindEvent
	}
	return kindLog
}
