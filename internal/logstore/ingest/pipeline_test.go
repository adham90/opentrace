package ingest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/fingerprint"
	"github.com/adham90/opentrace/internal/logstore/chunk"
)

func TestPIIScrubbing(t *testing.T) {
	pipeline := NewPipeline(nil, DefaultPIIConfig())

	body := json.RawMessage(`{
		"request": {
			"params": {"email": "user@example.com", "password": "secret123"},
			"headers": {"authorization": "Bearer tok_123"}
		},
		"queries": [{"sql": "SELECT * FROM users WHERE email = 'test@test.com'"}]
	}`)

	entries := []chunk.Entry{{
		Ts: 1000, Level: "info", Service: "api",
		Message: "POST /api/users 201 45ms",
		Body:    body,
	}}

	result := pipeline.Process(entries)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}

	scrubbed := string(result[0].Body)

	// Email should be scrubbed
	if strings.Contains(scrubbed, "user@example.com") {
		t.Error("email was not scrubbed from request params")
	}
	if strings.Contains(scrubbed, "test@test.com") {
		t.Error("email was not scrubbed from SQL query")
	}

	// Password field should be scrubbed
	if strings.Contains(scrubbed, "secret123") {
		t.Error("password value was not scrubbed")
	}

	// Authorization field should be scrubbed
	if strings.Contains(scrubbed, "Bearer tok_123") {
		t.Error("authorization value was not scrubbed")
	}

	// [FILTERED] should be present
	if !strings.Contains(scrubbed, "[FILTERED]") {
		t.Error("expected [FILTERED] in scrubbed body")
	}

	t.Logf("scrubbed body: %s", scrubbed)
}

func TestPIIScrubCreditCard(t *testing.T) {
	pipeline := NewPipeline(nil, DefaultPIIConfig())

	body := json.RawMessage(`{"payment": {"card": "4111 1111 1111 1111", "cvv": "123"}}`)
	entries := []chunk.Entry{{
		Ts: 1000, Level: "info", Service: "api", Message: "test", Body: body,
	}}

	result := pipeline.Process(entries)
	scrubbed := string(result[0].Body)

	if strings.Contains(scrubbed, "4111") {
		t.Error("credit card number was not scrubbed")
	}
}

func TestPIIDisabled(t *testing.T) {
	cfg := DefaultPIIConfig()
	cfg.Enabled = false
	pipeline := NewPipeline(nil, cfg)

	body := json.RawMessage(`{"email": "user@example.com"}`)
	entries := []chunk.Entry{{
		Ts: 1000, Level: "info", Service: "api", Message: "test", Body: body,
	}}

	result := pipeline.Process(entries)
	if !strings.Contains(string(result[0].Body), "user@example.com") {
		t.Error("PII scrubbing should be disabled but email was scrubbed")
	}
}

func TestErrorFingerprintComputation(t *testing.T) {
	pipeline := NewPipeline(nil, PIIConfig{})

	// SDK sends error fields as flat top-level fields (not extracted from body)
	entries := []chunk.Entry{{
		Ts: 1000, Level: "error", Service: "api",
		Message:    "NoMethodError: undefined method",
		ErrorClass: "NoMethodError",
		SourceFile: "app/models/order.rb",
		SourceLine: 42,
		Body:       json.RawMessage(`{"exception":{"backtrace":["app/models/order.rb:42"]}}`),
	}}

	result := pipeline.Process(entries)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}

	e := result[0]
	if e.ErrorClass != "NoMethodError" {
		t.Errorf("error_class: want %q, got %q", "NoMethodError", e.ErrorClass)
	}
	if e.SourceFile != "app/models/order.rb" {
		t.Errorf("source_file: want %q, got %q", "app/models/order.rb", e.SourceFile)
	}
	if e.SourceLine != 42 {
		t.Errorf("source_line: want 42, got %d", e.SourceLine)
	}
	if e.ErrorFingerprint == "" {
		t.Error("error_fingerprint should not be empty")
	}

	t.Logf("fingerprint: %s", e.ErrorFingerprint)
}

func TestErrorFingerprintNotOnInfo(t *testing.T) {
	pipeline := NewPipeline(nil, PIIConfig{})

	// Even if SDK sends error_class on an info entry, fingerprint should not be computed
	entries := []chunk.Entry{{
		Ts: 1000, Level: "info", Service: "api",
		Message:    "test",
		ErrorClass: "SomeError",
	}}

	result := pipeline.Process(entries)
	if result[0].ErrorFingerprint != "" {
		t.Error("should not compute fingerprint for info-level entries")
	}
}

func TestErrorFingerprintNoErrorClass(t *testing.T) {
	pipeline := NewPipeline(nil, PIIConfig{})

	// Error level but no error_class → no fingerprint
	entries := []chunk.Entry{{
		Ts: 1000, Level: "error", Service: "api",
		Message: "Something went wrong",
	}}

	result := pipeline.Process(entries)
	if result[0].ErrorFingerprint != "" {
		t.Error("should not compute fingerprint when no error_class")
	}
}

func TestInRequestLogExpansion(t *testing.T) {
	pipeline := NewPipeline(nil, PIIConfig{})

	body := json.RawMessage(`{
		"logs": [
			{"level": "info", "message": "Cache miss for user 42", "at": 10.5},
			{"level": "warn", "message": "Slow query detected", "at": 55.3}
		],
		"queries": [{"sql": "SELECT 1"}]
	}`)

	entries := []chunk.Entry{{
		Ts: 1743760800000, Level: "info", Service: "billing-api",
		Env: "production", Version: "a1b2c3d",
		Message:   "POST /api/orders 201 1243ms",
		TraceID:   "trace-xyz",
		RequestID: "req-abc",
		UserID:    "42",
		TenantID:  "7",
		Body:      body,
	}}

	result := pipeline.Process(entries)

	// 1 parent + 2 expanded = 3
	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}

	// Parent entry
	if result[0].Message != "POST /api/orders 201 1243ms" {
		t.Errorf("parent message: %q", result[0].Message)
	}

	// First expanded log
	child1 := result[1]
	if child1.Level != "info" {
		t.Errorf("child1 level: want info, got %q", child1.Level)
	}
	if child1.Message != "Cache miss for user 42" {
		t.Errorf("child1 message: %q", child1.Message)
	}
	if child1.EventType != "in_request_log" {
		t.Errorf("child1 event_type: want in_request_log, got %q", child1.EventType)
	}
	if child1.TraceID != "trace-xyz" {
		t.Errorf("child1 trace_id: want trace-xyz, got %q", child1.TraceID)
	}
	if child1.Service != "billing-api" {
		t.Errorf("child1 service: want billing-api, got %q", child1.Service)
	}
	// Timestamp should be parent + offset
	expectedTs := int64(1743760800000 + 10)
	if child1.Ts != expectedTs {
		t.Errorf("child1 ts: want %d, got %d", expectedTs, child1.Ts)
	}

	// Second expanded log
	child2 := result[2]
	if child2.Level != "warn" {
		t.Errorf("child2 level: want warn, got %q", child2.Level)
	}
	if child2.Message != "Slow query detected" {
		t.Errorf("child2 message: %q", child2.Message)
	}

	// Expanded entries should have NO body
	if len(child1.Body) > 0 {
		t.Error("expanded entries should not have body")
	}
}

func TestInRequestLogExpansionNoLogs(t *testing.T) {
	pipeline := NewPipeline(nil, PIIConfig{})

	body := json.RawMessage(`{"queries": [{"sql": "SELECT 1"}]}`)
	entries := []chunk.Entry{{
		Ts: 1000, Level: "info", Service: "api", Message: "test", Body: body,
	}}

	result := pipeline.Process(entries)
	if len(result) != 1 {
		t.Errorf("expected 1 entry (no expansion), got %d", len(result))
	}
}

func TestSampling(t *testing.T) {
	rules := []SamplingRule{
		{Service: "noisy-service", Rate: 0.0, KeepErrors: true},
		{Service: "*", Rate: 1.0},
	}
	pipeline := NewPipeline(rules, PIIConfig{})

	entries := []chunk.Entry{
		{Ts: 1, Level: "debug", Service: "noisy-service", Message: "dropped"},
		{Ts: 2, Level: "error", Service: "noisy-service", Message: "kept (error)"},
		{Ts: 3, Level: "info", Service: "other-service", Message: "kept (default rule)"},
	}

	result := pipeline.Process(entries)

	// Should keep: error from noisy-service + info from other-service = 2
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}

	if result[0].Level != "error" {
		t.Errorf("first kept: want error, got %q", result[0].Level)
	}
	if result[1].Service != "other-service" {
		t.Errorf("second kept: want other-service, got %q", result[1].Service)
	}
}

func TestSamplingNoRules(t *testing.T) {
	pipeline := NewPipeline(nil, PIIConfig{})

	entries := []chunk.Entry{
		{Ts: 1, Level: "debug", Service: "api", Message: "kept"},
		{Ts: 2, Level: "info", Service: "api", Message: "kept"},
	}

	result := pipeline.Process(entries)
	if len(result) != 2 {
		t.Errorf("expected 2 entries (no rules), got %d", len(result))
	}
}

func TestFullPipeline(t *testing.T) {
	// Test all steps together
	pipeline := NewPipeline(
		[]SamplingRule{{Service: "*", Rate: 1.0, KeepErrors: true}},
		DefaultPIIConfig(),
	)

	body := json.RawMessage(`{
		"exception": {"backtrace": ["app/services/billing.rb:99"]},
		"request": {"params": {"email": "user@example.com", "token": "secret_tok"}},
		"logs": [{"level": "debug", "message": "Charging card", "at": 5}]
	}`)

	// SDK sends error fields flat (not extracted from body by server)
	entries := []chunk.Entry{{
		Ts: 1743760800000, Level: "error", Service: "billing-api",
		Message:    "PaymentError: card declined",
		TraceID:    "trace-123",
		RequestID:  "req-456",
		ErrorClass: "PaymentError",
		SourceFile: "app/services/billing.rb",
		SourceLine: 99,
		Body:       body,
	}}

	result := pipeline.Process(entries)

	// 1 parent + 1 expanded log = 2
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}

	parent := result[0]

	// Error fields stay as sent by SDK
	if parent.ErrorClass != "PaymentError" {
		t.Errorf("error_class: %q", parent.ErrorClass)
	}
	if parent.SourceFile != "app/services/billing.rb" {
		t.Errorf("source_file: %q", parent.SourceFile)
	}
	if parent.SourceLine != 99 {
		t.Errorf("source_line: %d", parent.SourceLine)
	}
	// Server computes fingerprint from flat fields
	if parent.ErrorFingerprint == "" {
		t.Error("fingerprint should be set")
	}

	// PII scrubbed
	if strings.Contains(string(parent.Body), "user@example.com") {
		t.Error("email not scrubbed")
	}
	if strings.Contains(string(parent.Body), "secret_tok") {
		t.Error("token not scrubbed")
	}

	// Expanded log
	child := result[1]
	if child.EventType != "in_request_log" {
		t.Errorf("expanded event_type: %q", child.EventType)
	}
	if child.TraceID != "trace-123" {
		t.Errorf("expanded trace_id: %q", child.TraceID)
	}
}

// The pipeline stamps error_fingerprint on log entries; the error-group store keys
// its rows by the same value and joins the two on it. They ran different algorithms,
// so a group's "recent occurrences" lookup never matched a single log line. This
// pins the two to the shared definition.
func TestPipelineFingerprintMatchesSharedDefinition(t *testing.T) {
	e := &chunk.Entry{
		Level:      "error",
		Service:    "api",
		Message:    "undefined method `total' for nil",
		ErrorClass: "NoMethodError",
		SourceFile: "app/models/order.rb",
		SourceLine: 42,
	}
	computeErrorFingerprint(e)

	want := fingerprint.Compute("api", "NoMethodError", "app/models/order.rb", "undefined method `total' for nil")
	if e.ErrorFingerprint != want {
		t.Errorf("pipeline fingerprint = %q, shared definition = %q", e.ErrorFingerprint, want)
	}
	if e.ErrorFingerprint == "" {
		t.Error("fingerprint should be set for an error with a class and file")
	}
}

// Errors drift down a file whenever something is inserted above them. If the line
// number were part of the identity, an unrelated edit would fork one ongoing error
// into a fresh group with no history — right when the deploy that touched the file
// is what you want to correlate against.
func TestFingerprintSurvivesLineShift(t *testing.T) {
	at := func(line int) string {
		e := &chunk.Entry{
			Level: "error", Service: "api", Message: "boom",
			ErrorClass: "NoMethodError", SourceFile: "app/models/order.rb", SourceLine: line,
		}
		computeErrorFingerprint(e)
		return e.ErrorFingerprint
	}
	if at(42) != at(43) {
		t.Error("a line shift split the error into a new group")
	}
}

// Two different errors in the same file must stay separate, or a file's worth of
// unrelated failures collapses into one unactionable group.
func TestFingerprintSeparatesClassesInSameFile(t *testing.T) {
	at := func(class string) string {
		e := &chunk.Entry{
			Level: "error", Service: "api", Message: "boom",
			ErrorClass: class, SourceFile: "app/models/order.rb",
		}
		computeErrorFingerprint(e)
		return e.ErrorFingerprint
	}
	if at("NoMethodError") == at("ArgumentError") {
		t.Error("distinct error classes in one file collapsed into a single group")
	}
}

// TestScrubsMessage guards that PII in the log message (the most common leak
// location) is redacted, not just PII in the opaque body.
func TestScrubsMessage(t *testing.T) {
	p := NewPipeline(nil, PIIConfig{Enabled: true, ScrubEmails: true})
	out := p.Process([]chunk.Entry{{
		Level:   "info",
		Service: "svc",
		Message: "user login failed for alice@example.com",
	}})
	if len(out) != 1 {
		t.Fatalf("want 1 entry, got %d", len(out))
	}
	if strings.Contains(out[0].Message, "alice@example.com") {
		t.Fatalf("email not scrubbed from message: %q", out[0].Message)
	}
	if !strings.Contains(out[0].Message, filteredValue) {
		t.Fatalf("expected %q in scrubbed message, got %q", filteredValue, out[0].Message)
	}
}

// TestExpansionCapped guards that a body with a huge logs array can't expand
// into unbounded WAL entries.
func TestExpansionCapped(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"logs":[`)
	for i := 0; i < maxExpandedLogs+500; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"level":"info","message":"x"}`)
	}
	sb.WriteString(`]}`)

	p := NewPipeline(nil, PIIConfig{})
	out := p.Process([]chunk.Entry{{Level: "info", Service: "svc", Message: "parent", Body: []byte(sb.String())}})
	// 1 parent + at most maxExpandedLogs children.
	if len(out) > maxExpandedLogs+1 {
		t.Fatalf("expansion not capped: got %d entries (cap %d + parent)", len(out), maxExpandedLogs)
	}
	if len(out) != maxExpandedLogs+1 {
		t.Fatalf("want %d entries (parent + cap), got %d", maxExpandedLogs+1, len(out))
	}
}
