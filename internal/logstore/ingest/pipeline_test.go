package ingest

import (
	"encoding/json"
	"strings"
	"testing"

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
		Message:        "NoMethodError: undefined method",
		ExceptionClass: "NoMethodError",
		SourceFile:     "app/models/order.rb",
		SourceLine:     42,
		Body:           json.RawMessage(`{"exception":{"backtrace":["app/models/order.rb:42"]}}`),
	}}

	result := pipeline.Process(entries)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}

	e := result[0]
	if e.ExceptionClass != "NoMethodError" {
		t.Errorf("exception_class: want %q, got %q", "NoMethodError", e.ExceptionClass)
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

	// Even if SDK sends exception_class on an info entry, fingerprint should not be computed
	entries := []chunk.Entry{{
		Ts: 1000, Level: "info", Service: "api",
		Message:        "test",
		ExceptionClass: "SomeError",
	}}

	result := pipeline.Process(entries)
	if result[0].ErrorFingerprint != "" {
		t.Error("should not compute fingerprint for info-level entries")
	}
}

func TestErrorFingerprintNoExceptionClass(t *testing.T) {
	pipeline := NewPipeline(nil, PIIConfig{})

	// Error level but no exception_class → no fingerprint
	entries := []chunk.Entry{{
		Ts: 1000, Level: "error", Service: "api",
		Message: "Something went wrong",
	}}

	result := pipeline.Process(entries)
	if result[0].ErrorFingerprint != "" {
		t.Error("should not compute fingerprint when no exception_class")
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
		Message:        "PaymentError: card declined",
		TraceID:        "trace-123",
		RequestID:      "req-456",
		ExceptionClass: "PaymentError",
		SourceFile:     "app/services/billing.rb",
		SourceLine:     99,
		Body:           body,
	}}

	result := pipeline.Process(entries)

	// 1 parent + 1 expanded log = 2
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}

	parent := result[0]

	// Error fields stay as sent by SDK
	if parent.ExceptionClass != "PaymentError" {
		t.Errorf("exception_class: %q", parent.ExceptionClass)
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

func TestFingerprintDeterministic(t *testing.T) {
	fp1 := computeFingerprint("NoMethodError", "app/models/order.rb", 42)
	fp2 := computeFingerprint("NoMethodError", "app/models/order.rb", 42)
	if fp1 != fp2 {
		t.Errorf("fingerprints should be deterministic: %q vs %q", fp1, fp2)
	}

	fp3 := computeFingerprint("NoMethodError", "app/models/order.rb", 43)
	if fp1 == fp3 {
		t.Error("different line should produce different fingerprint")
	}
}
