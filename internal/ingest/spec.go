package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/adham90/opentrace/pkg/server"
)

// This file publishes the ingest wire contract at GET /spec so that a client
// can be written for any language without OpenTrace shipping an SDK for it.
// The field table is reflected off flatEntry's struct tags rather than
// maintained by hand: a published contract that can drift from the parser is
// worse than no contract at all, because it is believed.

// FieldSpec describes one field of the ingest wire format.
type FieldSpec struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
	Doc      string `json:"doc"`
}

// requiredFields are rejected with 400 when missing or empty.
var requiredFields = map[string]bool{"level": true, "message": true}

// recommendedFields are optional but cost real capability when omitted, so
// validate mode warns about them. See warnMissing in validate.go.
var recommendedFields = map[string]string{
	"ts":      "entries are stamped with server receive time, so anything buffered by the client lands at its flush time rather than when it happened",
	"service": "every query groups by service; entries without one are effectively unsearchable in a multi-service install",
}

// fieldCaps is the byte cap applied to each string field at ingest (see
// limits.go). Over-long values are truncated with a visible marker, never
// rejected. Kept in sync with flatToLogEntry by TestFieldCapsMatchIngest,
// which pushes an oversized value through the real mapper for every entry here.
var fieldCaps = map[string]int{
	"service":        maxIdentFieldBytes,
	"env":            maxIdentFieldBytes,
	"version":        maxIdentFieldBytes,
	"host":           maxIdentFieldBytes,
	"kind":           maxIdentFieldBytes,
	"event_type":     maxIdentFieldBytes,
	"trace_id":       maxIdentFieldBytes,
	"span_id":        maxIdentFieldBytes,
	"parent_span_id": maxIdentFieldBytes,
	"request_id":     maxIdentFieldBytes,
	"user_id":        maxIdentFieldBytes,
	"tenant_id":      maxIdentFieldBytes,
	"session_id":     maxIdentFieldBytes,
	"method":         maxIdentFieldBytes,
	"error_class":    maxIdentFieldBytes,
	"job_class":      maxIdentFieldBytes,
	"job_queue":      maxIdentFieldBytes,
	"job_id":         maxIdentFieldBytes,
	"path":           maxPathFieldBytes,
	"route":          maxPathFieldBytes,
	"handler":        maxPathFieldBytes,
	"source_file":    maxPathFieldBytes,
	"message":        maxMessageBytes,
	"error_message":  maxMessageBytes,
}

// uncappedStrings are string fields validated by shape instead of length: ts
// must parse as RFC3339, level must be one of validLevels.
var uncappedStrings = map[string]bool{"ts": true, "level": true}

// SpecFields returns the wire contract, reflected off flatEntry.
func SpecFields() []FieldSpec {
	t := reflect.TypeOf(flatEntry{})
	fields := make([]FieldSpec, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		fields = append(fields, FieldSpec{
			Name:     name,
			Type:     wireType(f.Type),
			Required: requiredFields[name],
			MaxBytes: fieldCaps[name],
			Doc:      f.Tag.Get("doc"),
		})
	}
	return fields
}

// fieldNames returns the set of accepted field names, for unknown-field
// detection in validate mode.
func fieldNames() map[string]bool {
	names := make(map[string]bool, len(SpecFields()))
	for _, f := range SpecFields() {
		names[f.Name] = true
	}
	return names
}

// wireType maps a Go field type to the JSON type a client must send.
func wireType(t reflect.Type) string {
	if t.Kind() == reflect.Ptr {
		return wireType(t.Elem())
	}
	switch t {
	case reflect.TypeOf(jsonInt(0)):
		return "integer"
	case reflect.TypeOf(jsonFloat(0)):
		return "number"
	case reflect.TypeOf(json.RawMessage(nil)):
		return "object"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	}
	return t.Kind().String()
}

// specExample is the golden payload: one request row and one error row, showing
// both the flat columns and the body blob.
const specExample = `[
  {
    "ts": "2026-04-04T10:15:30.123Z",
    "level": "info",
    "service": "checkout-api",
    "env": "production",
    "version": "a1b2c3d",
    "kind": "request",
    "event_type": "http.request",
    "message": "POST /api/checkout 200",
    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
    "request_id": "01JR2K8Q4N",
    "user_id": "usr_8812",
    "method": "POST",
    "path": "/api/checkout",
    "route": "/api/checkout",
    "handler": "CheckoutController#create",
    "status": 200,
    "duration_ms": 412,
    "db_ms": 180,
    "db_count": 23,
    "ext_ms": 95,
    "ext_count": 1,
    "n_plus_one": true,
    "dup_queries": 18,
    "body": {
      "params": {"cart_id": "c_991"},
      "queries": [{"sql": "SELECT * FROM line_items WHERE cart_id = ?", "ms": 4, "count": 18}]
    }
  },
  {
    "ts": "2026-04-04T10:15:30.540Z",
    "level": "error",
    "service": "checkout-api",
    "env": "production",
    "version": "a1b2c3d",
    "message": "undefined method 'zip' for nil",
    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
    "request_id": "01JR2K8Q4N",
    "user_id": "usr_8812",
    "error_class": "NoMethodError",
    "error_message": "undefined method 'zip' for nil:NilClass",
    "source_file": "app/controllers/payments_controller.rb",
    "source_line": 87,
    "body": {
      "handled": false,
      "backtrace": "app/controllers/payments_controller.rb:87:in 'charge'\napp/controllers/checkout_controller.rb:24:in 'create'",
      "source_context": {"85": "  def charge", "86": "    addr = customer.address", "87": "    addr.zip"},
      "params": {"cart_id": "c_991"}
    }
  }
]`

// HandleSpec serves the ingest contract: markdown by default (it is read by
// coding agents and humans, both of which do better with prose than with a
// schema), JSON with ?format=json for programmatic use.
func HandleSpec(w http.ResponseWriter, r *http.Request) {
	base := requestBaseURL(r)
	if r.URL.Query().Get("format") == "json" {
		server.WriteJSON(w, http.StatusOK, map[string]any{
			"endpoint":     base + "/api/v2/logs",
			"validate":     base + "/api/v2/logs?validate=1",
			"method":       "POST",
			"auth":         "Authorization: Bearer <api-key>",
			"content_type": "application/json",
			"encodings":    []string{"identity", "gzip"},
			"max_body_mb":  10,
			"levels":       strings.Split(validLevelList, ", "),
			"kinds":        []string{kindLog, kindRequest, kindJob, kindEvent},
			"fields":       SpecFields(),
			"example":      json.RawMessage(specExample),
		})
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = io.WriteString(w, SpecMarkdown(base))
}

// requestBaseURL reconstructs the externally-visible origin so the examples in
// the spec are copy-pasteable rather than placeholders.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	if host == "" {
		host = "your-server.com"
	}
	return scheme + "://" + host
}

// SpecMarkdown renders the full contract. base is the server's own origin.
func SpecMarkdown(base string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `# OpenTrace ingest spec

Everything OpenTrace stores arrives through one endpoint. There is no SDK
requirement and no adapter to install: any language that can POST JSON is a
first-class client, and this document is the whole contract.

    POST %s/api/v2/logs
    Authorization: Bearer <api-key>
    Content-Type: application/json

The body is one entry object, or an array of them. Success is `+"`201`"+` with
`+"`{\"count\": N}`"+`, where N can be lower than what you sent if server-side
sampling rules dropped rows (`+"`\"sampled\": true`"+` is then also set, and a batch
sampled away entirely answers `+"`200 {\"count\": 0}`"+`). Neither is a failure —
do not retry on them.

## Writing a client

A correct client is about a hundred lines. The rules that matter, in order of
how much damage getting them wrong does:

1. **Never block the request path.** Append to an in-memory buffer and flush
   from a background worker, on a timer (~5s) or when the buffer reaches ~100
   entries.
2. **Never raise into the host application.** A failed flush drops the batch and
   logs nothing to stderr in a loop. Observability must not be able to take down
   the process it observes.
3. **Bound the buffer** (~1000 entries) and drop oldest when it is full. An
   unbounded queue turns a slow network into an out-of-memory kill.
4. **Flush on exit**, best-effort, with a short timeout.
5. **Send `+"`X-Batch-ID`"+`**: a fresh UUID per batch, reused unchanged on retry. The
   server dedupes by that id, so a retry after a timeout cannot double-write.
   Retry at most twice with backoff, then drop the batch.
6. **gzip is supported** (`+"`Content-Encoding: gzip`"+`) and worth it above ~10 entries.
   The limit is 10MB decompressed.
7. **Scrub before you send.** The server strips well-known PII patterns, but it
   cannot know which of your fields are secret.

## Check the client before you ship it

    POST %s/api/v2/logs?validate=1

Same payload, same auth, nothing stored. The response reports, for every entry:
what the server parsed, which fields it did not recognise (with a suggested
correction), which values would be truncated, and which fields would be silently
dropped for this row's kind. It always returns 200 — read `+"`valid`"+` in the body.

Send one representative entry of each kind you emit, fix everything reported,
repeat until `+"`\"valid\": true`"+` with an empty `+"`warnings`"+`, then drop the parameter.
This loop is the supported way to bring up a new client.

## Fields

Unknown fields are ignored rather than rejected, so a typo costs you the data
silently — this is exactly what validate mode is for. Anything the columns below
do not cover belongs in `+"`body`"+`, which is stored whole and searchable.

`, base, base)

	b.WriteString("| field | type | required | max bytes | notes |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, f := range SpecFields() {
		req, max := "", ""
		if f.Required {
			req = "yes"
		}
		if f.MaxBytes > 0 {
			max = fmt.Sprintf("%d", f.MaxBytes)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n",
			f.Name, f.Type, req, max, strings.ReplaceAll(f.Doc, "|", "\\|"))
	}

	fmt.Fprintf(&b, `
Values longer than the cap are truncated with a visible `+"`…[truncated]`"+` marker.
Levels outside the list reject the whole batch with 400.

## Kind

`+"`kind`"+` decides which columns a row keeps, so it is worth sending explicitly.
When omitted it is inferred: `+"`event_type: http.request`"+` or any of method /
path / route / status means `+"`request`"+`; otherwise any job_* field means `+"`job`"+`;
otherwise a bare `+"`event_type`"+` means `+"`event`"+`; otherwise `+"`log`"+`.

Fields stored **only** on `+"`kind=request`"+` rows: `+"`method`"+`, `+"`path`"+`, `+"`handler`"+`,
`+"`n_plus_one`"+`, `+"`dup_queries`"+`. Send them on a `+"`log`"+` row and they are dropped —
validate mode says so.

## Example

%s

`+"```json\n%s\n```"+`

## Errors

- **400** rejects the **entire batch** and names the first problem found. It is
  never worth retrying unchanged. Validate mode reports every problem in every
  entry at once; prefer it while developing.
- **401** means the Bearer key is wrong or missing. Not retryable.
- **429** means you are over the rate limit. Back off; do not drop the batch yet.
- **5xx** and network failures are retryable, with the same `+"`X-Batch-ID`"+`.
`, "A request row and the error it produced, correlated by `trace_id`:", specExample)

	return b.String()
}
