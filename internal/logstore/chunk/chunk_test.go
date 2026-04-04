package chunk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestChunkWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chunk_000.col")

	tr := true
	entries := makeTestEntries()

	// Write
	w, err := NewWriter(path, entries)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.WriteAllColumns(); err != nil {
		t.Fatalf("WriteAllColumns: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Check file exists and has reasonable size
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	t.Logf("chunk file size: %d bytes for %d entries", fi.Size(), len(entries))

	// Read
	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()

	if r.EntryCount != len(entries) {
		t.Fatalf("entry count: want %d, got %d", len(entries), r.EntryCount)
	}

	// Verify id column (delta+varint)
	ids, err := r.ReadDeltaInt64("id")
	if err != nil {
		t.Fatalf("ReadDeltaInt64(id): %v", err)
	}
	for i, e := range entries {
		if ids[i] != e.ID {
			t.Errorf("id[%d]: want %d, got %d", i, e.ID, ids[i])
		}
	}

	// Verify ts column (zstd int64)
	ts, err := r.ReadZstdInt64("ts")
	if err != nil {
		t.Fatalf("ReadZstdInt64(ts): %v", err)
	}
	for i, e := range entries {
		if ts[i] != e.Ts {
			t.Errorf("ts[%d]: want %d, got %d", i, e.Ts, ts[i])
		}
	}

	// Verify received_at column (delta+varint)
	recvAt, err := r.ReadDeltaInt64("received_at")
	if err != nil {
		t.Fatalf("ReadDeltaInt64(received_at): %v", err)
	}
	for i, e := range entries {
		if recvAt[i] != e.ReceivedAt {
			t.Errorf("received_at[%d]: want %d, got %d", i, e.ReceivedAt, recvAt[i])
		}
	}

	// Verify level column (dict+bitpack)
	levels, err := r.ReadDictBitpack("level")
	if err != nil {
		t.Fatalf("ReadDictBitpack(level): %v", err)
	}
	for i, e := range entries {
		if levels[i] != e.Level {
			t.Errorf("level[%d]: want %q, got %q", i, e.Level, levels[i])
		}
	}

	// Verify service column (dict+zstd)
	services, err := r.ReadDictZstd("service")
	if err != nil {
		t.Fatalf("ReadDictZstd(service): %v", err)
	}
	for i, e := range entries {
		if services[i] != e.Service {
			t.Errorf("service[%d]: want %q, got %q", i, e.Service, services[i])
		}
	}

	// Verify message column (zstd block)
	messages, err := r.ReadZstdBlockStrings("message")
	if err != nil {
		t.Fatalf("ReadZstdBlockStrings(message): %v", err)
	}
	for i, e := range entries {
		if messages[i] != e.Message {
			t.Errorf("message[%d]: want %q, got %q", i, e.Message, messages[i])
		}
	}

	// Verify trace_id column (sparse string)
	traceIDs, err := r.ReadSparseStrings("trace_id")
	if err != nil {
		t.Fatalf("ReadSparseStrings(trace_id): %v", err)
	}
	for i, e := range entries {
		if traceIDs[i] != e.TraceID {
			t.Errorf("trace_id[%d]: want %q, got %q", i, e.TraceID, traceIDs[i])
		}
	}

	// Verify duration_ms column (sparse int64)
	durations, err := r.ReadSparseInt64("duration_ms")
	if err != nil {
		t.Fatalf("ReadSparseInt64(duration_ms): %v", err)
	}
	for i, e := range entries {
		if e.DurationMs > 0 {
			if durations[i] == nil || *durations[i] != int64(e.DurationMs) {
				t.Errorf("duration_ms[%d]: want %d, got %v", i, e.DurationMs, durations[i])
			}
		} else {
			if durations[i] != nil {
				t.Errorf("duration_ms[%d]: want nil, got %d", i, *durations[i])
			}
		}
	}

	// Verify n_plus_one column (sparse bool)
	nPlusOnes, err := r.ReadSparseBool("n_plus_one")
	if err != nil {
		t.Fatalf("ReadSparseBool(n_plus_one): %v", err)
	}
	for i, e := range entries {
		if e.NPlusOne != nil {
			if nPlusOnes[i] == nil || *nPlusOnes[i] != *e.NPlusOne {
				t.Errorf("n_plus_one[%d]: want %v, got %v", i, *e.NPlusOne, nPlusOnes[i])
			}
		} else {
			if nPlusOnes[i] != nil {
				t.Errorf("n_plus_one[%d]: want nil, got %v", i, *nPlusOnes[i])
			}
		}
	}

	// Verify body column (sparse bytes)
	bodies, err := r.ReadSparseBytes("body")
	if err != nil {
		t.Fatalf("ReadSparseBytes(body): %v", err)
	}
	for i, e := range entries {
		if len(e.Body) > 0 {
			if bodies[i] == nil {
				t.Errorf("body[%d]: want non-nil, got nil", i)
			} else if string(bodies[i]) != string(e.Body) {
				t.Errorf("body[%d]: content mismatch", i)
			}
		} else {
			if bodies[i] != nil {
				t.Errorf("body[%d]: want nil, got %d bytes", i, len(bodies[i]))
			}
		}
	}

	// Verify error tracking fields
	excClasses, err := r.ReadSparseStrings("error_class")
	if err != nil {
		t.Fatalf("ReadSparseStrings(error_class): %v", err)
	}
	for i, e := range entries {
		if excClasses[i] != e.ErrorClass {
			t.Errorf("error_class[%d]: want %q, got %q", i, e.ErrorClass, excClasses[i])
		}
	}

	// Verify HasColumn
	if !r.HasColumn("id") {
		t.Error("HasColumn(id) should be true")
	}
	if r.HasColumn("nonexistent_column") {
		t.Error("HasColumn(nonexistent_column) should be false")
	}

	// Verify NullCount
	traceNulls := r.NullCount("trace_id")
	expectedTraceNulls := 0
	for _, e := range entries {
		if e.TraceID == "" {
			expectedTraceNulls++
		}
	}
	if traceNulls != expectedTraceNulls {
		t.Errorf("NullCount(trace_id): want %d, got %d", expectedTraceNulls, traceNulls)
	}

	// Schema evolution: missing column returns all nulls
	missingNulls := r.NullCount("future_column")
	if missingNulls != len(entries) {
		t.Errorf("NullCount(future_column): want %d, got %d", len(entries), missingNulls)
	}

	_ = tr // used above
}

func TestChunkReadColumn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chunk_000.col")

	entries := makeTestEntries()
	w, err := NewWriter(path, entries)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.WriteAllColumns(); err != nil {
		t.Fatalf("WriteAllColumns: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()

	// Test ReadColumn generic dispatch for various types
	for _, col := range Schema {
		result, err := r.ReadColumn(col.Name)
		if err != nil {
			t.Errorf("ReadColumn(%s): %v", col.Name, err)
			continue
		}
		if result == nil {
			t.Errorf("ReadColumn(%s): got nil", col.Name)
		}
	}

	// Missing column should return nil, nil
	result, err := r.ReadColumn("future_column")
	if err != nil {
		t.Errorf("ReadColumn(future_column): %v", err)
	}
	if result != nil {
		t.Errorf("ReadColumn(future_column): expected nil, got %T", result)
	}
}

func TestChunkEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.col")

	entries := []Entry{}
	w, err := NewWriter(path, entries)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.WriteAllColumns(); err != nil {
		t.Fatalf("WriteAllColumns: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()

	if r.EntryCount != 0 {
		t.Errorf("expected 0 entries, got %d", r.EntryCount)
	}
}

func TestChunkSingleEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.col")

	entries := []Entry{{
		ID: 1, Ts: 1743760800000, ReceivedAt: 1743760800001,
		Level: "info", Service: "api", Message: "hello",
	}}

	w, err := NewWriter(path, entries)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.WriteAllColumns(); err != nil {
		t.Fatalf("WriteAllColumns: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()

	if r.EntryCount != 1 {
		t.Errorf("expected 1 entry, got %d", r.EntryCount)
	}

	messages, err := r.ReadZstdBlockStrings("message")
	if err != nil {
		t.Fatalf("ReadZstdBlockStrings: %v", err)
	}
	if messages[0] != "hello" {
		t.Errorf("message: want %q, got %q", "hello", messages[0])
	}
}

// makeTestEntries creates a mix of simple, structured, and rich entries.
func makeTestEntries() []Entry {
	tr := true
	fa := false
	body := json.RawMessage(`{"queries":[{"sql":"SELECT 1","duration_ms":1.2}],"timeline":[{"t":"db","ms":1.2}]}`)

	return []Entry{
		// Simple log
		{
			ID: 100, Ts: 1743760800000, ReceivedAt: 1743760800001,
			Level: "info", Service: "billing-api", Env: "production",
			Message: "Cache hit for user 42",
		},
		// Error log with trace
		{
			ID: 101, Ts: 1743760800012, ReceivedAt: 1743760800013,
			Level: "error", Service: "billing-api", Env: "production",
			Message:          "Failed to charge customer: card declined",
			TraceID:          "trace-xyz789",
			RequestID:        "req-abc123",
			UserID:           "42",
			TenantID:         "7",
			ErrorClass:   "PaymentError",
			SourceFile:       "app/services/billing.rb",
			SourceLine:       42,
			ErrorFingerprint: "a8f3c2",
		},
		// Structured log
		{
			ID: 102, Ts: 1743760800015, ReceivedAt: 1743760800016,
			Level: "warn", Service: "auth", Env: "production",
			Message:   "Slow query detected: 287ms",
			EventType: "db.slow_query",
			TraceID:   "trace-abc123",
			RequestID: "req-def456",
		},
		// Rich request with body
		{
			ID: 103, Ts: 1743760800060, ReceivedAt: 1743760800061,
			Level: "info", Service: "billing-api", Env: "production",
			Version:     "a1b2c3d",
			Message:     "POST /api/orders 201 1243ms",
			EventType:   "http.request",
			TraceID:     "trace-xyz789",
			SpanID:      "span-001",
			RequestID:   "req-abc123",
			UserID:      "42",
			TenantID:    "7",
			SessionID:   "sess-def456",
			Method:      "POST",
			Path:        "/api/orders",
			Status:      201,
			DurationMs:  1243,
			Handler: "Api::OrdersController#create",
			
			DbMs:        312,
			DbCount:     8,
			NPlusOne:    &fa,
			SlowQueries: 1,
			Body:        body,
		},
		// Another simple log
		{
			ID: 104, Ts: 1743760800100, ReceivedAt: 1743760800101,
			Level: "debug", Service: "worker",
			Message: "Job started: ProcessOrderJob",
		},
		// Rich request with n_plus_one
		{
			ID: 105, Ts: 1743760800200, ReceivedAt: 1743760800201,
			Level: "info", Service: "billing-api", Env: "production",
			Message:    "GET /api/users 200 456ms",
			EventType:  "http.request",
			TraceID:    "trace-999",
			RequestID:  "req-999",
			Method:     "GET",
			Path:       "/api/users",
			Status:     200,
			DurationMs: 456,
			Handler: "Api::UsersController#index",
			
			DbMs:       300,
			DbCount:    42,
			NPlusOne:   &tr,
			DupQueries: 15,
		},
	}
}
