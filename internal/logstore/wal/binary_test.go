package wal

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

func TestMarshalUnmarshalSimpleLog(t *testing.T) {
	e := &chunk.Entry{
		ID: 100, Ts: 1743760800000, ReceivedAt: 1743760800001,
		Level: "error", Service: "billing-api",
		Message: "Failed to charge customer: card declined",
	}

	data := MarshalEntry(e)
	t.Logf("simple log: %d bytes", len(data))

	// Skip the 4-byte length prefix for UnmarshalEntry
	decoded, err := UnmarshalEntry(data[4:])
	if err != nil {
		t.Fatalf("UnmarshalEntry: %v", err)
	}

	assertEntryEqual(t, e, decoded)
}

func TestMarshalUnmarshalStructuredLog(t *testing.T) {
	e := &chunk.Entry{
		ID: 101, Ts: 1743760800012, ReceivedAt: 1743760800013,
		Level: "warn", Service: "auth", Env: "production",
		Message:   "Slow query detected: 287ms on order_items",
		EventType: "db.slow_query",
		TraceID:   "trace-abc123",
		RequestID: "req-def456",
		UserID:    "42",
		TenantID:  "7",
	}

	data := MarshalEntry(e)
	t.Logf("structured log: %d bytes", len(data))

	decoded, err := UnmarshalEntry(data[4:])
	if err != nil {
		t.Fatalf("UnmarshalEntry: %v", err)
	}

	assertEntryEqual(t, e, decoded)
}

func TestMarshalUnmarshalRichRequest(t *testing.T) {
	body := json.RawMessage(`{"queries":[{"sql":"SELECT 1"}],"timeline":[{"t":"db","ms":1.2}]}`)

	e := &chunk.Entry{
		ID: 103, Ts: 1743760800060, ReceivedAt: 1743760800061,
		Level: "info", Service: "billing-api", Env: "production",
		Version:          "a1b2c3d",
		Message:          "POST /api/orders 201 1243ms",
		EventType:        "http.request",
		TraceID:          "trace-xyz789",
		SpanID:           "span-001",
		ParentSpanID:     "parent-001",
		RequestID:        "req-abc123",
		UserID:           "42",
		TenantID:         "7",
		SessionID:        "sess-def456",
		Method:           "POST",
		Path:             "/api/orders",
		Status:           201,
		DurationMs:       1243,
		Handler:       "Api::OrdersController#create",
		DbMs:             312,
		DbCount:          8,
		SlowQueries:      1,
		DupQueries:       3,
		ErrorClass:       "",
		SourceFile:       "",
		SourceLine:       0,
		ErrorFingerprint: "",
		Body:             body,
	}

	data := MarshalEntry(e)
	t.Logf("rich request: %d bytes (body %d bytes)", len(data), len(body))

	decoded, err := UnmarshalEntry(data[4:])
	if err != nil {
		t.Fatalf("UnmarshalEntry: %v", err)
	}

	assertEntryEqual(t, e, decoded)

	// Verify body content matches
	if string(decoded.Body) != string(body) {
		t.Errorf("body mismatch:\n  want: %s\n  got:  %s", body, decoded.Body)
	}
}

func TestMarshalUnmarshalErrorEntry(t *testing.T) {
	e := &chunk.Entry{
		ID: 102, Ts: 1743760800015, ReceivedAt: 1743760800016,
		Level: "error", Service: "billing-api", Env: "production",
		Message:          "NoMethodError: undefined method 'name' for nil",
		TraceID:          "trace-xyz789",
		RequestID:        "req-abc123",
		ErrorClass:   "NoMethodError",
		SourceFile:       "app/models/order.rb",
		SourceLine:       42,
		ErrorFingerprint: "a8f3c2d1",
	}

	data := MarshalEntry(e)
	t.Logf("error entry: %d bytes", len(data))

	decoded, err := UnmarshalEntry(data[4:])
	if err != nil {
		t.Fatalf("UnmarshalEntry: %v", err)
	}

	assertEntryEqual(t, e, decoded)
}

func TestMarshalUnmarshalMinimalEntry(t *testing.T) {
	e := &chunk.Entry{
		ID: 1, Ts: 1000, ReceivedAt: 1001,
		Level: "debug", Service: "x", Message: "y",
	}

	data := MarshalEntry(e)
	t.Logf("minimal entry: %d bytes", len(data))

	decoded, err := UnmarshalEntry(data[4:])
	if err != nil {
		t.Fatalf("UnmarshalEntry: %v", err)
	}

	assertEntryEqual(t, e, decoded)
}

func TestReadEntries(t *testing.T) {
	entries := []*chunk.Entry{
		{ID: 1, Ts: 1000, ReceivedAt: 1001, Level: "info", Service: "api", Message: "hello"},
		{ID: 2, Ts: 2000, ReceivedAt: 2001, Level: "error", Service: "api", Message: "boom"},
		{ID: 3, Ts: 3000, ReceivedAt: 3001, Level: "debug", Service: "worker", Message: "tick"},
	}

	// Write all entries to a buffer
	var buf bytes.Buffer
	for _, e := range entries {
		data := MarshalEntry(e)
		buf.Write(data)
	}

	// Read them back
	decoded, err := ReadEntries(&buf)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}

	if len(decoded) != len(entries) {
		t.Fatalf("entry count: want %d, got %d", len(entries), len(decoded))
	}

	for i, want := range entries {
		assertEntryEqual(t, want, &decoded[i])
	}
}

func TestSizeComparison(t *testing.T) {
	// Compare binary format size vs JSON for the same entries
	body := json.RawMessage(`{"queries":[{"sql":"SELECT * FROM users WHERE id = ?","duration_ms":1.2}]}`)

	entries := []*chunk.Entry{
		{
			ID: 100, Ts: 1743760800000, ReceivedAt: 1743760800001,
			Level: "info", Service: "billing-api",
			Message: "Cache hit for user 42",
		},
		{
			ID: 101, Ts: 1743760800012, ReceivedAt: 1743760800013,
			Level: "info", Service: "billing-api", Env: "production",
			Version: "a1b2c3d", Message: "POST /api/orders 201 1243ms",
			EventType: "http.request", TraceID: "trace-xyz789",
			RequestID: "req-abc123", Method: "POST", Path: "/api/orders",
			Status: 201, DurationMs: 1243, Handler:       "Api::OrdersController#create",
			Body: body,
		},
	}

	for _, e := range entries {
		binaryData := MarshalEntry(e)
		jsonData, _ := json.Marshal(e)
		ratio := float64(len(jsonData)) / float64(len(binaryData))
		t.Logf("entry %d: binary=%d bytes, json=%d bytes (%.1fx smaller)",
			e.ID, len(binaryData), len(jsonData), ratio)
	}
}

func assertEntryEqual(t *testing.T, want, got *chunk.Entry) {
	t.Helper()
	if want.ID != got.ID {
		t.Errorf("ID: want %d, got %d", want.ID, got.ID)
	}
	if want.Ts != got.Ts {
		t.Errorf("Ts: want %d, got %d", want.Ts, got.Ts)
	}
	if want.ReceivedAt != got.ReceivedAt {
		t.Errorf("ReceivedAt: want %d, got %d", want.ReceivedAt, got.ReceivedAt)
	}
	if want.Level != got.Level {
		t.Errorf("Level: want %q, got %q", want.Level, got.Level)
	}
	if want.Service != got.Service {
		t.Errorf("Service: want %q, got %q", want.Service, got.Service)
	}
	if want.Env != got.Env {
		t.Errorf("Env: want %q, got %q", want.Env, got.Env)
	}
	if want.Version != got.Version {
		t.Errorf("Version: want %q, got %q", want.Version, got.Version)
	}
	if want.Message != got.Message {
		t.Errorf("Message: want %q, got %q", want.Message, got.Message)
	}
	if want.EventType != got.EventType {
		t.Errorf("EventType: want %q, got %q", want.EventType, got.EventType)
	}
	if want.TraceID != got.TraceID {
		t.Errorf("TraceID: want %q, got %q", want.TraceID, got.TraceID)
	}
	if want.SpanID != got.SpanID {
		t.Errorf("SpanID: want %q, got %q", want.SpanID, got.SpanID)
	}
	if want.ParentSpanID != got.ParentSpanID {
		t.Errorf("ParentSpanID: want %q, got %q", want.ParentSpanID, got.ParentSpanID)
	}
	if want.RequestID != got.RequestID {
		t.Errorf("RequestID: want %q, got %q", want.RequestID, got.RequestID)
	}
	if want.UserID != got.UserID {
		t.Errorf("UserID: want %q, got %q", want.UserID, got.UserID)
	}
	if want.TenantID != got.TenantID {
		t.Errorf("TenantID: want %q, got %q", want.TenantID, got.TenantID)
	}
	if want.SessionID != got.SessionID {
		t.Errorf("SessionID: want %q, got %q", want.SessionID, got.SessionID)
	}
	if want.Method != got.Method {
		t.Errorf("Method: want %q, got %q", want.Method, got.Method)
	}
	if want.Path != got.Path {
		t.Errorf("Path: want %q, got %q", want.Path, got.Path)
	}
	if want.Status != got.Status {
		t.Errorf("Status: want %d, got %d", want.Status, got.Status)
	}
	if want.DurationMs != got.DurationMs {
		t.Errorf("DurationMs: want %d, got %d", want.DurationMs, got.DurationMs)
	}
	if want.Handler != got.Handler {
		t.Errorf("Handler: want %q, got %q", want.Handler, got.Handler)
	}
	if want.DbMs != got.DbMs {
		t.Errorf("DbMs: want %d, got %d", want.DbMs, got.DbMs)
	}
	if want.DbCount != got.DbCount {
		t.Errorf("DbCount: want %d, got %d", want.DbCount, got.DbCount)
	}
	if (want.NPlusOne == nil) != (got.NPlusOne == nil) {
		t.Errorf("NPlusOne nil: want %v, got %v", want.NPlusOne == nil, got.NPlusOne == nil)
	} else if want.NPlusOne != nil && *want.NPlusOne != *got.NPlusOne {
		t.Errorf("NPlusOne: want %v, got %v", *want.NPlusOne, *got.NPlusOne)
	}
	if want.SlowQueries != got.SlowQueries {
		t.Errorf("SlowQueries: want %d, got %d", want.SlowQueries, got.SlowQueries)
	}
	if want.DupQueries != got.DupQueries {
		t.Errorf("DupQueries: want %d, got %d", want.DupQueries, got.DupQueries)
	}
	if want.ErrorClass != got.ErrorClass {
		t.Errorf("ErrorClass: want %q, got %q", want.ErrorClass, got.ErrorClass)
	}
	if want.SourceFile != got.SourceFile {
		t.Errorf("SourceFile: want %q, got %q", want.SourceFile, got.SourceFile)
	}
	if want.SourceLine != got.SourceLine {
		t.Errorf("SourceLine: want %d, got %d", want.SourceLine, got.SourceLine)
	}
	if want.ErrorFingerprint != got.ErrorFingerprint {
		t.Errorf("ErrorFingerprint: want %q, got %q", want.ErrorFingerprint, got.ErrorFingerprint)
	}
}
