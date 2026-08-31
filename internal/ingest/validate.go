package ingest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/server"
)

// Validate mode: POST /api/v2/logs?validate=1 parses a payload exactly as the
// real endpoint would, stores nothing, and reports everything that would go
// wrong or be silently dropped. It exists so a client written for a language
// OpenTrace ships no SDK for can be brought up in a correction loop instead of
// by guessing — which is what "unknown fields are ignored" otherwise forces.
//
// Two things it deliberately does differently from the real endpoint: it
// reports every problem in every entry rather than rejecting the batch at the
// first one, and it answers 200 even when the payload is invalid, so the caller
// reads the report instead of an error page.

// validateReport is the response body of a dry-run request.
type validateReport struct {
	Valid   bool            `json:"valid"`
	Entries int             `json:"entries"`
	Stored  bool            `json:"stored"`
	Spec    string          `json:"spec"`
	Results []validateEntry `json:"results"`
}

// validateEntry is the per-entry section of the report.
type validateEntry struct {
	Index    int            `json:"index"`
	Errors   []string       `json:"errors,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
	Unknown  []unknownField `json:"unknown_fields,omitempty"`
	Kind     string         `json:"kind,omitempty"`
	Stored   map[string]any `json:"would_store,omitempty"`
}

// unknownField is a key the server does not recognise and would drop.
type unknownField struct {
	Field      string `json:"field"`
	DidYouMean string `json:"did_you_mean,omitempty"`
}

// isValidateRequest reports whether the request asked for a dry run. Presence
// of the parameter is enough (?validate, ?validate=1, ?validate=true); an
// explicit falsey value opts back out.
func isValidateRequest(r *http.Request) bool {
	v, ok := r.URL.Query()["validate"]
	if !ok {
		return false
	}
	if len(v) > 0 {
		switch strings.ToLower(v[0]) {
		case "0", "false", "no", "off":
			return false
		}
	}
	return true
}

// splitEntries accepts either a single entry object or an array of them, the
// two shapes both ingest paths have always taken. Shared by the real handler
// and validate mode so a payload can never parse differently between them.
func splitEntries(body []byte) ([]json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, fmt.Errorf("empty request body: send one entry object or an array of them")
	}
	if trimmed[0] == '{' {
		return []json.RawMessage{json.RawMessage(trimmed)}, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	return raw, nil
}

// handleValidate runs the dry run and writes the report.
func (h *Handler) handleValidate(w http.ResponseWriter, body []byte) {
	raw, err := splitEntries(body)
	if err != nil {
		server.WriteJSON(w, http.StatusOK, validateReport{
			Spec: "GET /spec",
			Results: []validateEntry{{
				Index:  0,
				Errors: []string{err.Error()},
			}},
		})
		return
	}

	report := validateReport{Valid: true, Entries: len(raw), Spec: "GET /spec"}
	if len(raw) == 0 {
		report.Valid = false
		report.Results = append(report.Results, validateEntry{
			Errors: []string{"the batch is an empty array: nothing would be stored"},
		})
	}
	for i, rm := range raw {
		res := h.validateOne(i, rm)
		if len(res.Errors) > 0 {
			report.Valid = false
		}
		report.Results = append(report.Results, res)
	}
	server.WriteJSON(w, http.StatusOK, report)
}

// validateOne reports on a single entry. It never returns early on a warning —
// the point is to hand back every problem in one round trip.
func (h *Handler) validateOne(index int, rm json.RawMessage) validateEntry {
	res := validateEntry{Index: index}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(rm, &keys); err != nil {
		res.Errors = append(res.Errors, "entry must be a JSON object: "+err.Error())
		return res
	}

	// Per-key type checks against the published spec. Done key-by-key rather
	// than by decoding the struct because encoding/json stops at the first type
	// error, and one report per round trip defeats the purpose.
	specs := make(map[string]FieldSpec, len(SpecFields()))
	for _, f := range SpecFields() {
		specs[f.Name] = f
	}
	for _, k := range sortedKeys(keys) {
		spec, known := specs[k]
		if !known {
			res.Unknown = append(res.Unknown, unknownField{Field: k, DidYouMean: suggestField(k)})
			continue
		}
		if msg := checkWireType(spec.Type, keys[k]); msg != "" {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %s", k, msg))
		}
	}

	var fe flatEntry
	if err := json.Unmarshal(rm, &fe); err != nil {
		if len(res.Errors) == 0 {
			res.Errors = append(res.Errors, "decode failed: "+err.Error())
			return res
		}
		// The per-key pass already named the offending fields. Re-decode without
		// them so the rest of the report — warnings, inferred kind, dropped
		// fields — still comes back in this round trip, instead of the caller
		// discovering one class of problem per attempt.
		clean := make(map[string]json.RawMessage, len(keys))
		for k, v := range keys {
			if spec, known := specs[k]; known && checkWireType(spec.Type, v) == "" {
				clean[k] = v
			}
		}
		b, err := json.Marshal(clean)
		if err != nil {
			return res
		}
		fe = flatEntry{}
		if err := json.Unmarshal(b, &fe); err != nil {
			return res
		}
	}

	if fe.Level == "" {
		res.Errors = append(res.Errors, "level: required, and must be one of "+validLevelList)
	} else {
		lower := strings.ToLower(fe.Level)
		switch {
		case !isValidLevel(lower):
			res.Errors = append(res.Errors, fmt.Sprintf("level: %q is not one of %s", fe.Level, validLevelList))
		case lower != fe.Level:
			res.Warnings = append(res.Warnings, fmt.Sprintf("level: %q is stored lowercased as %q", fe.Level, lower))
		case lower == "warning":
			res.Warnings = append(res.Warnings, `level: "warning" is stored as "warn" — filters must query warn`)
		}
	}
	if fe.Message == "" {
		res.Errors = append(res.Errors, "message: required and must be non-empty")
	}

	ts := time.Now().UTC()
	if fe.Ts != "" {
		t, err := time.Parse(time.RFC3339Nano, fe.Ts)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("ts: %q is not RFC3339 (want 2026-04-04T10:15:30.123Z)", fe.Ts))
		} else {
			ts = t
			switch d := time.Since(t); {
			case d > 48*time.Hour:
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"ts: %s is %s in the past — retention prunes by this value, so check the client is not flushing a stale buffer", fe.Ts, d.Round(time.Hour)))
			case d < -1*time.Hour:
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"ts: %s is %s in the future — check the clock and the timezone; queries default to a window ending now", fe.Ts, (-d).Round(time.Hour)))
			}
		}
	}

	for _, name := range sortedRecommended() {
		if _, sent := keys[name]; !sent {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: not sent — %s", name, recommendedFields[name]))
		}
	}

	res.Warnings = append(res.Warnings, truncationWarnings(fe)...)

	kind := deriveKind(fe)
	res.Kind = kind
	if fe.Kind == "" {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"kind: not sent — inferred as %q from the fields present; send it explicitly if that is not what you meant", kind))
	}
	if kind != kindRequest {
		for _, f := range requestOnlyFields {
			if _, sent := keys[f]; sent {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"%s: dropped — only stored on kind=request rows, and this row is kind=%s", f, kind))
			}
		}
	}
	if len(fe.Body) > 0 && strings.TrimSpace(string(fe.Body))[0] != '{' {
		res.Errors = append(res.Errors, "body: must be a JSON object — wrap arrays and scalars in one")
	}

	if len(res.Errors) == 0 {
		res.Stored = storedView(h.flatToLogEntry(fe, ts))
	}
	return res
}

// requestOnlyFields are accepted on any entry but only reach storage on
// kind=request rows (see flatToLogEntry's RequestSummary branch).
var requestOnlyFields = []string{"method", "path", "handler", "n_plus_one", "dup_queries"}

// truncationWarnings reports string fields that exceed their ingest cap.
func truncationWarnings(fe flatEntry) []string {
	var out []string
	v := reflect.ValueOf(fe)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Type.Kind() != reflect.String {
			continue
		}
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		limit, capped := fieldCaps[name]
		if !capped {
			continue
		}
		if n := len(v.Field(i).String()); n > limit {
			out = append(out, fmt.Sprintf("%s: %d bytes, truncated to %d", name, n, limit))
		}
	}
	return out
}

// storedView renders the canonical row this entry would become, so a client
// author can see field renames (version becomes commit_hash), unit conversions
// (mem_delta_mb is scaled) and the derived error fingerprint rather than
// inferring them.
func storedView(entry any) map[string]any {
	b, err := json.Marshal(entry)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	// Storage-assigned columns carry no information before the insert.
	for _, k := range []string{"id", "created_at"} {
		delete(m, k)
	}
	if rs, ok := m["request_summary"].(map[string]any); ok {
		delete(rs, "id")
		delete(rs, "log_id")
	}
	return m
}

// fieldAliases maps names other logging ecosystems use onto ours. A generated
// client reaches for its own framework's vocabulary first, and a plain edit
// distance does not connect "duration" to "duration_ms" or "@timestamp" to "ts".
var fieldAliases = map[string]string{
	"timestamp": "ts", "time": "ts", "@timestamp": "ts", "datetime": "ts", "date": "ts", "when": "ts",
	"severity": "level", "levelname": "level", "log_level": "level", "loglevel": "level", "priority": "level",
	"msg": "message", "text": "message", "content": "message", "summary": "message",
	"logger": "service", "app": "service", "application": "service", "service_name": "service", "component": "service",
	"environment": "env", "stage": "env", "deployment": "env",
	"release": "version", "commit": "version", "sha": "version", "revision": "version", "build": "version",
	"hostname": "host", "server": "host", "pod": "host", "container": "host", "instance": "host",
	"duration": "duration_ms", "elapsed": "duration_ms", "latency": "duration_ms", "took": "duration_ms",
	"response_time": "duration_ms", "runtime": "duration_ms",
	"status_code": "status", "http_status": "status", "response_status": "status",
	"url": "path", "uri": "path", "request_path": "path", "endpoint": "route", "pattern": "route",
	"verb": "method", "http_method": "method", "request_method": "method",
	"controller": "handler", "action": "handler", "function": "handler", "operation": "handler",
	"user": "user_id", "uid": "user_id", "actor": "user_id", "current_user": "user_id",
	"account_id": "tenant_id", "org_id": "tenant_id", "organization_id": "tenant_id", "workspace_id": "tenant_id",
	"trace": "trace_id", "traceid": "trace_id", "correlation_id": "request_id", "req_id": "request_id",
	"span": "span_id", "spanid": "span_id", "parent_id": "parent_span_id",
	"exception": "error_class", "error_type": "error_class", "exception_class": "error_class", "class": "error_class",
	"error": "error_message", "err": "error_message", "exception_message": "error_message", "reason": "error_message",
	"file": "source_file", "filename": "source_file", "path_to_file": "source_file",
	"line": "source_line", "lineno": "source_line", "line_number": "source_line",
	"queue": "job_queue", "worker": "job_class", "job": "job_class", "task": "job_class",
	"db_time": "db_ms", "sql_time": "db_ms", "sql_count": "db_count", "query_count": "db_count",
	"metadata": "body", "extra": "body", "fields": "body", "attributes": "body", "attrs": "body",
	"context": "body", "data": "body", "payload": "body", "details": "body", "queries": "body",
	"stack": "body.backtrace", "stacktrace": "body.backtrace", "backtrace": "body.backtrace",
	"stack_trace": "body.backtrace", "traceback": "body.backtrace", "params": "body.params",
}

// suggestField proposes the field an unrecognised key probably meant: a known
// alias first, then the nearest accepted name by edit distance.
func suggestField(key string) string {
	k := strings.ToLower(strings.TrimSpace(key))
	if target, ok := fieldAliases[k]; ok {
		if nested := strings.SplitN(target, ".", 2); len(nested) == 2 {
			return fmt.Sprintf("%s, as %q inside it", nested[0], nested[1])
		}
		return target
	}
	best, bestDist := "", 3 // 3 = beyond this, a suggestion is noise
	for name := range fieldNames() {
		if d := editDistance(k, name); d < bestDist {
			best, bestDist = name, d
		}
	}
	return best
}

// editDistance is Levenshtein over two short ASCII field names.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, min(curr[j-1]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// checkWireType reports why a raw JSON value cannot be the declared type, or ""
// when it can. Approximate by design: json.Unmarshal is still run afterwards
// and backstops anything missed here.
func checkWireType(want string, raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	got := jsonKindOf(s)
	if got == want {
		return ""
	}
	switch want {
	case "integer", "number":
		if got != "number" {
			if got == "string" {
				return fmt.Sprintf("expected a JSON number, got a string — send %s, not %s", strings.Trim(s, `"`), s)
			}
			return "expected a JSON number, got " + got
		}
	case "string":
		return "expected a JSON string, got " + got
	case "boolean":
		return "expected true or false, got " + got
	case "object":
		return "expected a JSON object, got " + got
	}
	return ""
}

// jsonKindOf names the JSON type of an already-trimmed, non-empty value.
func jsonKindOf(s string) string {
	switch s[0] {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "an array"
	case 't', 'f':
		return "boolean"
	}
	return "number"
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedRecommended() []string {
	out := make([]string, 0, len(recommendedFields))
	for k := range recommendedFields {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
