package ingest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// postValidate runs a dry-run request against a handler with NO store wired in.
// That is the assertion, not an omission: if validate mode ever reaches
// storage, this panics instead of silently writing during a client's bring-up
// loop.
func postValidate(t *testing.T, body string) validateReport {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v2/logs?validate=1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	(&Handler{}).HandleFlatIngest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("validate must answer 200 so the report is readable, got %d: %s", rec.Code, rec.Body.String())
	}
	var report validateReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("report is not valid JSON: %v", err)
	}
	if report.Stored {
		t.Fatalf("validate mode reported storing the payload")
	}
	return report
}

func TestValidate_CleanEntry(t *testing.T) {
	// Relative, not a fixture date: validate mode warns about timestamps far
	// from now, which is exactly the check a hardcoded date would trip.
	report := postValidate(t, `{
		"ts": "`+time.Now().UTC().Format(time.RFC3339Nano)+`",
		"level": "error",
		"service": "checkout-api",
		"env": "production",
		"message": "undefined method 'zip' for nil",
		"error_class": "NoMethodError",
		"source_file": "app/controllers/payments_controller.rb",
		"source_line": 87,
		"kind": "log",
		"body": {"handled": false}
	}`)

	if !report.Valid {
		t.Fatalf("valid entry rejected: %+v", report.Results)
	}
	res := report.Results[0]
	if len(res.Warnings) > 0 || len(res.Unknown) > 0 {
		t.Errorf("unexpected warnings %v / unknown %+v", res.Warnings, res.Unknown)
	}
	// would_store shows the caller the renames and derived values it cannot
	// guess: version becomes commit_hash, errors gain a fingerprint.
	if res.Stored["error_fingerprint"] == nil {
		t.Errorf("would_store omitted the derived fingerprint: %v", res.Stored)
	}
	if res.Stored["exception_class"] != "NoMethodError" {
		t.Errorf("would_store did not show error_class landing as exception_class: %v", res.Stored)
	}
}

func TestValidate_ReportsEveryProblemAtOnce(t *testing.T) {
	// One entry with a wrong-name field, a wrong-type field, a missing required
	// field and a field that only applies to another kind. A client author must
	// see all four in one response, or bring-up costs one round trip per typo.
	report := postValidate(t, `[{
		"timestamp": "2026-04-04T10:15:30Z",
		"level": "INFO",
		"duration": 42,
		"status": "200",
		"n_plus_one": true
	}]`)

	if report.Valid {
		t.Fatalf("invalid entry accepted")
	}
	res := report.Results[0]
	joined := strings.Join(append(res.Errors, res.Warnings...), " | ")

	if !strings.Contains(joined, "message") {
		t.Errorf("did not report the missing required message: %s", joined)
	}
	if !strings.Contains(joined, "status") {
		t.Errorf("did not report status sent as a string: %s", joined)
	}
	if !strings.Contains(joined, "n_plus_one") {
		t.Errorf("did not warn that n_plus_one is dropped on a non-request row: %s", joined)
	}
	if !strings.Contains(joined, "service") {
		t.Errorf("did not warn about the missing service: %s", joined)
	}
	if !strings.Contains(strings.ToLower(joined), "lowercased") {
		t.Errorf("did not warn that INFO is stored lowercased: %s", joined)
	}

	suggestions := map[string]string{}
	for _, u := range res.Unknown {
		suggestions[u.Field] = u.DidYouMean
	}
	if suggestions["timestamp"] != "ts" {
		t.Errorf("timestamp: want suggestion %q, got %q", "ts", suggestions["timestamp"])
	}
	if suggestions["duration"] != "duration_ms" {
		t.Errorf("duration: want suggestion %q, got %q", "duration_ms", suggestions["duration"])
	}
	if res.Stored != nil {
		t.Errorf("would_store must be omitted while the entry has errors")
	}
}

func TestValidate_SuggestsNestingUnderBody(t *testing.T) {
	report := postValidate(t, `{"level":"error","message":"boom","stacktrace":"a\nb","extra":{"k":1}}`)

	got := map[string]string{}
	for _, u := range report.Results[0].Unknown {
		got[u.Field] = u.DidYouMean
	}
	if !strings.HasPrefix(got["stacktrace"], "body") || !strings.Contains(got["stacktrace"], "backtrace") {
		t.Errorf("stacktrace: want a body.backtrace suggestion, got %q", got["stacktrace"])
	}
	if got["extra"] != "body" {
		t.Errorf("extra: want %q, got %q", "body", got["extra"])
	}
}

func TestValidate_TruncationAndKindInference(t *testing.T) {
	report := postValidate(t, `{
		"level": "info",
		"service": "`+strings.Repeat("s", 600)+`",
		"message": "GET /users/1",
		"method": "GET",
		"path": "/users/1"
	}`)

	res := report.Results[0]
	joined := strings.Join(res.Warnings, " | ")
	if !strings.Contains(joined, "600 bytes, truncated to 512") {
		t.Errorf("did not warn about the over-long service: %s", joined)
	}
	if res.Kind != kindRequest {
		t.Errorf("kind: want %q inferred from method/path, got %q", kindRequest, res.Kind)
	}
	if !strings.Contains(joined, "inferred") {
		t.Errorf("did not warn that kind was inferred: %s", joined)
	}
}

func TestValidate_MalformedPayload(t *testing.T) {
	for name, body := range map[string]string{
		"not json":    `{"level":`,
		"empty":       ``,
		"empty batch": `[]`,
		"scalar":      `["just a string"]`,
	} {
		t.Run(name, func(t *testing.T) {
			report := postValidate(t, body)
			if report.Valid {
				t.Errorf("accepted a malformed payload")
			}
			if len(report.Results) == 0 || len(report.Results[0].Errors) == 0 {
				t.Errorf("no error explaining the rejection: %+v", report.Results)
			}
		})
	}
}

// TestValidate_NotEnabledByDefault guards the parameter parsing: a normal
// ingest POST must still store, and only an explicit opt-out turns validate off.
func TestValidate_QueryParsing(t *testing.T) {
	cases := map[string]bool{
		"/api/v2/logs":                false,
		"/api/v2/logs?validate":       true,
		"/api/v2/logs?validate=1":     true,
		"/api/v2/logs?validate=true":  true,
		"/api/v2/logs?validate=0":     false,
		"/api/v2/logs?validate=false": false,
	}
	for url, want := range cases {
		if got := isValidateRequest(httptest.NewRequest("POST", url, nil)); got != want {
			t.Errorf("%s: want validate=%v, got %v", url, want, got)
		}
	}
}
