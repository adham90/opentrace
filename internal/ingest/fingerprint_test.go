package ingest

import (
	"testing"

	"github.com/adham90/opentrace/internal/fingerprint"
	"github.com/adham90/opentrace/pkg/store"
)

// The error-group path and the columnar log pipeline must agree on a fingerprint,
// because groups are joined to their occurrences by that value alone. They each had
// their own hash for a while, so every group's "recent occurrences" was empty. The
// pipeline side of this invariant is pinned in internal/logstore/ingest.
func TestGenerateErrorFingerprint_MatchesSharedDefinition(t *testing.T) {
	e := &store.LogEntry{
		Level:          "error",
		Service:        "api",
		Message:        "undefined method `total' for nil",
		ExceptionClass: "NoMethodError",
		SourceFile:     "app/models/order.rb",
		SourceLine:     42,
	}
	got := GenerateErrorFingerprint(e)
	want := fingerprint.Compute("api", "NoMethodError", "app/models/order.rb", "undefined method `total' for nil")
	if got != want {
		t.Errorf("group fingerprint = %q, shared definition = %q", got, want)
	}
}

func TestGenerateErrorFingerprint_BasicFields(t *testing.T) {
	e := &store.LogEntry{
		Level:          "error",
		Service:        "payment-api",
		ExceptionClass: "NilPointerError",
		SourceFile:     "app/controllers/payments_controller.rb",
		Message:        "undefined method `charge' for nil:NilClass",
	}

	fp := GenerateErrorFingerprint(e)
	if fp == "" {
		t.Fatal("expected non-empty fingerprint")
	}
	if len(fp) != 16 {
		t.Errorf("fingerprint length = %d, want 16", len(fp))
	}

	// Same error again should produce the same fingerprint.
	e2 := &store.LogEntry{
		Level:          "error",
		Service:        "payment-api",
		ExceptionClass: "NilPointerError",
		SourceFile:     "app/controllers/payments_controller.rb",
		Message:        "undefined method `charge' for nil:NilClass at user 99999",
	}
	fp2 := GenerateErrorFingerprint(e2)
	if fp != fp2 {
		t.Errorf("same error at same file should produce same fingerprint: %q != %q", fp, fp2)
	}
}

func TestGenerateErrorFingerprint_DifferentLinesSameFile(t *testing.T) {
	e1 := &store.LogEntry{
		Level:          "error",
		Service:        "api",
		ExceptionClass: "TypeError",
		SourceFile:     "app/models/user.rb",
		SourceLine:     42,
		Message:        "wrong type",
	}
	e2 := &store.LogEntry{
		Level:          "error",
		Service:        "api",
		ExceptionClass: "TypeError",
		SourceFile:     "app/models/user.rb",
		SourceLine:     99, // different line, same file
		Message:        "wrong type",
	}

	// Should produce same fingerprint — line number is excluded.
	fp1 := GenerateErrorFingerprint(e1)
	fp2 := GenerateErrorFingerprint(e2)
	if fp1 != fp2 {
		t.Errorf("same exception at same file (different lines) should match: %q != %q", fp1, fp2)
	}
}

func TestGenerateErrorFingerprint_DifferentServiceSeparates(t *testing.T) {
	base := store.LogEntry{
		Level:          "error",
		ExceptionClass: "NilPointerError",
		SourceFile:     "app/controllers/payments_controller.rb",
		Message:        "oops",
	}

	e1 := base
	e1.Service = "payment-api"
	e2 := base
	e2.Service = "checkout-api"

	fp1 := GenerateErrorFingerprint(&e1)
	fp2 := GenerateErrorFingerprint(&e2)
	if fp1 == fp2 {
		t.Error("same error in different services should produce different fingerprints")
	}
}

func TestGenerateErrorFingerprint_FallsBackToMessage(t *testing.T) {
	e := &store.LogEntry{
		Level:          "error",
		Service:        "api",
		ExceptionClass: "RuntimeError",
		SourceFile:     "", // no source file
		Message:        "connection refused to database-host:5432",
	}

	fp := GenerateErrorFingerprint(e)
	if fp == "" {
		t.Fatal("expected non-empty fingerprint even without source_file")
	}

	// Same message with different dynamic values should match.
	e2 := &store.LogEntry{
		Level:          "error",
		Service:        "api",
		ExceptionClass: "RuntimeError",
		SourceFile:     "",
		Message:        "connection refused to database-host:9999",
	}
	fp2 := GenerateErrorFingerprint(e2)
	if fp != fp2 {
		t.Errorf("messages differing only in port number should match: %q != %q", fp, fp2)
	}
}

func TestGenerateErrorFingerprint_ExtractsFromBacktrace(t *testing.T) {
	// No source_file set, but backtrace in metadata.
	e := &store.LogEntry{
		Level:          "error",
		Service:        "api",
		ExceptionClass: "NoMethodError",
		Message:        "undefined method for nil",
		Metadata: map[string]any{
			"backtrace": []any{
				"app/controllers/orders_controller.rb:42:in `create'",
				"app/controllers/application_controller.rb:10:in `call'",
			},
		},
	}

	fp := GenerateErrorFingerprint(e)
	if fp == "" {
		t.Fatal("expected non-empty fingerprint from backtrace")
	}

	// Compare with explicit source_file — should match.
	e2 := &store.LogEntry{
		Level:          "error",
		Service:        "api",
		ExceptionClass: "NoMethodError",
		SourceFile:     "app/controllers/orders_controller.rb",
		Message:        "undefined method for nil",
	}
	fp2 := GenerateErrorFingerprint(e2)
	if fp != fp2 {
		t.Errorf("backtrace extraction should match explicit source_file: %q != %q", fp, fp2)
	}
}

func TestGenerateErrorFingerprint_NonErrorLevelReturnsEmpty(t *testing.T) {
	e := &store.LogEntry{
		Level:          "info",
		Service:        "api",
		ExceptionClass: "SomeClass",
		SourceFile:     "app/foo.rb",
		Message:        "some message",
	}

	fp := GenerateErrorFingerprint(e)
	if fp != "" {
		t.Errorf("non-error level should return empty fingerprint, got %q", fp)
	}
}

func TestGenerateErrorFingerprint_FatalLevel(t *testing.T) {
	e := &store.LogEntry{
		Level:          "fatal",
		Service:        "api",
		ExceptionClass: "SystemExit",
		SourceFile:     "app/worker.rb",
		Message:        "worker died",
	}

	fp := GenerateErrorFingerprint(e)
	if fp == "" {
		t.Fatal("fatal level should produce a fingerprint")
	}
}

func TestNormalizeMessage(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "UUIDs replaced",
			input: "User 550e8400-e29b-41d4-a716-446655440000 not found",
			want:  "User <UUID> not found",
		},
		{
			name:  "numbers replaced",
			input: "connection refused on port 5432",
			want:  "connection refused on port <N>",
		},
		{
			name:  "quoted strings replaced",
			input: `column "user_email" does not exist`,
			want:  `column <STR> does not exist`,
		},
		{
			name:  "IP addresses replaced",
			input: "connection to 192.168.1.100:5432 refused",
			want:  "connection to <IP> refused",
		},
		{
			name:  "URL paths with IDs replaced",
			input: "GET /users/42 returned 404",
			want:  "GET /<PATH>/<N> returned <N>",
		},
		{
			name:  "short words preserved",
			input: "nil for 0",
			want:  "nil for 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeMessage(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeMessage(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractFileFromFrame(t *testing.T) {
	tests := []struct {
		name  string
		frame string
		want  string
	}{
		{
			name:  "Ruby frame",
			frame: "app/controllers/payments_controller.rb:87:in `charge'",
			want:  "app/controllers/payments_controller.rb",
		},
		{
			name:  "Node frame with parens",
			frame: "    at PaymentsController.charge (app/controllers/payments.js:87:12)",
			want:  "app/controllers/payments.js",
		},
		{
			name:  "Node frame without parens",
			frame: "    at app/controllers/payments.js:87:12",
			want:  "app/controllers/payments.js",
		},
		{
			name:  "Python frame",
			frame: `File "app/controllers/payments.py", line 87, in charge`,
			want:  "app/controllers/payments.py",
		},
		{
			name:  "Go frame",
			frame: "app/controllers/payments.go:87",
			want:  "app/controllers/payments.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFileFromFrame(tt.frame)
			if got != tt.want {
				t.Errorf("extractFileFromFrame(%q) = %q, want %q", tt.frame, got, tt.want)
			}
		})
	}
}

func TestGenerateErrorFingerprint_ConsistentAcrossLanguages(t *testing.T) {
	// Ruby and Node errors for the same logical error should produce the same fingerprint
	// when they have the same service, exception class, and source file.

	rubyEntry := &store.LogEntry{
		Level:          "error",
		Service:        "api",
		ExceptionClass: "TypeError",
		SourceFile:     "app/services/payment_service.rb",
		Message:        "no implicit conversion of nil into String",
		Metadata: map[string]any{
			"backtrace": []any{
				"app/services/payment_service.rb:42:in `process'",
			},
		},
	}

	// Node would have different backtrace format but same source_file extracted
	nodeEntry := &store.LogEntry{
		Level:          "error",
		Service:        "api",
		ExceptionClass: "TypeError",
		SourceFile:     "app/services/payment_service.rb", // same file
		Message:        "Cannot read properties of null (reading 'process')",
		Metadata: map[string]any{
			"stack_trace": "TypeError: Cannot read properties\n    at PaymentService.process (app/services/payment_service.rb:42:12)",
		},
	}

	fpRuby := GenerateErrorFingerprint(rubyEntry)
	fpNode := GenerateErrorFingerprint(nodeEntry)

	if fpRuby != fpNode {
		t.Errorf("same service + error_class + source_file should produce same fingerprint across languages: ruby=%q node=%q", fpRuby, fpNode)
	}
}
