package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestTraceStore_Upsert_NewTrace(t *testing.T) {
	db := setupTestDB(t)
	s := NewTraceStore(db)
	ctx := context.Background()

	entry := LogEntry{
		TraceID:      "trace-001",
		SpanID:       "span-001",
		ParentSpanID: "span-000",
		Service:      "api-gateway",
		Level:        "INFO",
		Timestamp:    time.Now().UTC(),
	}

	if err := s.UpsertTraceStatus(ctx, entry.TraceID, entry); err != nil {
		t.Fatalf("UpsertTraceStatus: %v", err)
	}

	ts, err := s.GetTraceStatus(ctx, "trace-001")
	if err != nil {
		t.Fatalf("GetTraceStatus: %v", err)
	}
	if ts.SpanCount != 1 {
		t.Errorf("SpanCount = %d, want 1", ts.SpanCount)
	}
	if ts.RootSpanID != "" {
		t.Errorf("RootSpanID = %q, want empty (not root)", ts.RootSpanID)
	}
	if len(ts.Services) != 1 || ts.Services[0] != "api-gateway" {
		t.Errorf("Services = %v, want [api-gateway]", ts.Services)
	}
	if ts.Status != "partial" {
		t.Errorf("Status = %q, want partial", ts.Status)
	}
	if ts.HasErrors {
		t.Error("HasErrors = true, want false")
	}
}

func TestTraceStore_Upsert_IncrementsSpanCount(t *testing.T) {
	db := setupTestDB(t)
	s := NewTraceStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entry1 := LogEntry{
		TraceID:      "trace-002",
		SpanID:       "span-a",
		ParentSpanID: "span-root",
		Service:      "frontend",
		Level:        "INFO",
		Timestamp:    now,
	}
	entry2 := LogEntry{
		TraceID:      "trace-002",
		SpanID:       "span-b",
		ParentSpanID: "span-root",
		Service:      "frontend",
		Level:        "INFO",
		Timestamp:    now.Add(100 * time.Millisecond),
	}
	entry3 := LogEntry{
		TraceID:      "trace-002",
		SpanID:       "span-c",
		ParentSpanID: "span-a",
		Service:      "backend",
		Level:        "ERROR",
		Timestamp:    now.Add(200 * time.Millisecond),
	}

	for _, e := range []LogEntry{entry1, entry2, entry3} {
		if err := s.UpsertTraceStatus(ctx, e.TraceID, e); err != nil {
			t.Fatalf("UpsertTraceStatus: %v", err)
		}
	}

	ts, err := s.GetTraceStatus(ctx, "trace-002")
	if err != nil {
		t.Fatalf("GetTraceStatus: %v", err)
	}
	if ts.SpanCount != 3 {
		t.Errorf("SpanCount = %d, want 3", ts.SpanCount)
	}
}

func TestTraceStore_Upsert_DetectsRootSpan(t *testing.T) {
	db := setupTestDB(t)
	s := NewTraceStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// First: a child span (has parent)
	child := LogEntry{
		TraceID:      "trace-003",
		SpanID:       "span-child",
		ParentSpanID: "span-root",
		Service:      "api",
		Level:        "INFO",
		Timestamp:    now,
	}
	if err := s.UpsertTraceStatus(ctx, child.TraceID, child); err != nil {
		t.Fatalf("UpsertTraceStatus child: %v", err)
	}

	ts, err := s.GetTraceStatus(ctx, "trace-003")
	if err != nil {
		t.Fatalf("GetTraceStatus: %v", err)
	}
	if ts.RootSpanID != "" {
		t.Errorf("RootSpanID after child = %q, want empty", ts.RootSpanID)
	}

	// Second: the root span (no parent)
	root := LogEntry{
		TraceID:      "trace-003",
		SpanID:       "span-root",
		ParentSpanID: "",
		Service:      "api",
		Level:        "INFO",
		Timestamp:    now.Add(10 * time.Millisecond),
	}
	if err := s.UpsertTraceStatus(ctx, root.TraceID, root); err != nil {
		t.Fatalf("UpsertTraceStatus root: %v", err)
	}

	ts, err = s.GetTraceStatus(ctx, "trace-003")
	if err != nil {
		t.Fatalf("GetTraceStatus: %v", err)
	}
	if ts.RootSpanID != "span-root" {
		t.Errorf("RootSpanID = %q, want span-root", ts.RootSpanID)
	}
}

func TestTraceStore_Upsert_TracksMultipleServices(t *testing.T) {
	db := setupTestDB(t)
	s := NewTraceStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	services := []string{"api-gateway", "user-service", "payment-service"}

	for i, svc := range services {
		entry := LogEntry{
			TraceID:      "trace-004",
			SpanID:       "span-" + svc,
			ParentSpanID: "span-root",
			Service:      svc,
			Level:        "INFO",
			Timestamp:    now.Add(time.Duration(i) * time.Millisecond),
		}
		if err := s.UpsertTraceStatus(ctx, entry.TraceID, entry); err != nil {
			t.Fatalf("UpsertTraceStatus %s: %v", svc, err)
		}
	}

	ts, err := s.GetTraceStatus(ctx, "trace-004")
	if err != nil {
		t.Fatalf("GetTraceStatus: %v", err)
	}
	if len(ts.Services) != 3 {
		t.Errorf("Services count = %d, want 3; got %v", len(ts.Services), ts.Services)
	}

	// Upsert same service again — should not duplicate
	entry := LogEntry{
		TraceID:      "trace-004",
		SpanID:       "span-dup",
		ParentSpanID: "span-root",
		Service:      "api-gateway",
		Level:        "INFO",
		Timestamp:    now.Add(10 * time.Millisecond),
	}
	if err := s.UpsertTraceStatus(ctx, entry.TraceID, entry); err != nil {
		t.Fatalf("UpsertTraceStatus duplicate: %v", err)
	}

	ts, err = s.GetTraceStatus(ctx, "trace-004")
	if err != nil {
		t.Fatalf("GetTraceStatus: %v", err)
	}
	if len(ts.Services) != 3 {
		t.Errorf("Services count after dup = %d, want 3; got %v", len(ts.Services), ts.Services)
	}
}

func TestTraceStore_Upsert_TracksErrors(t *testing.T) {
	db := setupTestDB(t)
	s := NewTraceStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// First: INFO span
	info := LogEntry{
		TraceID:      "trace-005",
		SpanID:       "span-ok",
		ParentSpanID: "span-root",
		Service:      "api",
		Level:        "INFO",
		Timestamp:    now,
	}
	if err := s.UpsertTraceStatus(ctx, info.TraceID, info); err != nil {
		t.Fatalf("UpsertTraceStatus: %v", err)
	}
	ts, _ := s.GetTraceStatus(ctx, "trace-005")
	if ts.HasErrors {
		t.Error("HasErrors should be false after INFO span")
	}

	// Second: ERROR span
	errEntry := LogEntry{
		TraceID:      "trace-005",
		SpanID:       "span-err",
		ParentSpanID: "span-root",
		Service:      "api",
		Level:        "error",
		Timestamp:    now.Add(50 * time.Millisecond),
	}
	if err := s.UpsertTraceStatus(ctx, errEntry.TraceID, errEntry); err != nil {
		t.Fatalf("UpsertTraceStatus: %v", err)
	}
	ts, _ = s.GetTraceStatus(ctx, "trace-005")
	if !ts.HasErrors {
		t.Error("HasErrors should be true after ERROR span")
	}
}

func TestTraceStore_MarkStaleTraces(t *testing.T) {
	db := setupTestDB(t)
	s := NewTraceStore(db)
	ctx := context.Background()

	// Insert two traces: one "old" and one "recent"
	oldEntry := LogEntry{
		TraceID:   "trace-old",
		SpanID:    "span-old",
		Service:   "svc",
		Level:     "INFO",
		Timestamp: time.Now().UTC(),
	}
	if err := s.UpsertTraceStatus(ctx, oldEntry.TraceID, oldEntry); err != nil {
		t.Fatalf("UpsertTraceStatus old: %v", err)
	}

	// Manually backdate the old trace's last_updated_at
	_, err := db.ExecContext(ctx,
		`UPDATE trace_status SET last_updated_at = ? WHERE trace_id = ?`,
		time.Now().UTC().Add(-2*time.Minute).Format(time.RFC3339), "trace-old",
	)
	if err != nil {
		t.Fatalf("backdate: %v", err)
	}

	recentEntry := LogEntry{
		TraceID:   "trace-recent",
		SpanID:    "span-recent",
		Service:   "svc",
		Level:     "INFO",
		Timestamp: time.Now().UTC(),
	}
	if err := s.UpsertTraceStatus(ctx, recentEntry.TraceID, recentEntry); err != nil {
		t.Fatalf("UpsertTraceStatus recent: %v", err)
	}

	// Mark traces older than 30 seconds as timeout
	n, err := s.MarkStaleTraces(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("MarkStaleTraces: %v", err)
	}
	if n != 1 {
		t.Errorf("MarkStaleTraces count = %d, want 1", n)
	}

	// Verify old trace is now timeout
	ts, _ := s.GetTraceStatus(ctx, "trace-old")
	if ts.Status != "timeout" {
		t.Errorf("old trace status = %q, want timeout", ts.Status)
	}

	// Verify recent trace is still partial
	ts, _ = s.GetTraceStatus(ctx, "trace-recent")
	if ts.Status != "partial" {
		t.Errorf("recent trace status = %q, want partial", ts.Status)
	}
}

func TestTraceStore_GetTraceStatus_NotFound(t *testing.T) {
	db := setupTestDB(t)
	s := NewTraceStore(db)
	ctx := context.Background()

	_, err := s.GetTraceStatus(ctx, "nonexistent-trace")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestTraceStore_ListRecentTraces_Pagination(t *testing.T) {
	db := setupTestDB(t)
	s := NewTraceStore(db)
	ctx := context.Background()

	// Insert 5 traces
	for i := 0; i < 5; i++ {
		entry := LogEntry{
			TraceID:   fmt.Sprintf("trace-%03d", i),
			SpanID:    fmt.Sprintf("span-%03d", i),
			Service:   "svc",
			Level:     "INFO",
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
		}
		if err := s.UpsertTraceStatus(ctx, entry.TraceID, entry); err != nil {
			t.Fatalf("UpsertTraceStatus %d: %v", i, err)
		}
	}

	// Page 1: limit=2, offset=0
	traces, total, err := s.ListRecentTraces(ctx, 2, 0)
	if err != nil {
		t.Fatalf("ListRecentTraces: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(traces) != 2 {
		t.Errorf("page 1 len = %d, want 2", len(traces))
	}

	// Page 2: limit=2, offset=2
	traces, total, err = s.ListRecentTraces(ctx, 2, 2)
	if err != nil {
		t.Fatalf("ListRecentTraces page 2: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(traces) != 2 {
		t.Errorf("page 2 len = %d, want 2", len(traces))
	}

	// Page 3: limit=2, offset=4
	traces, _, err = s.ListRecentTraces(ctx, 2, 4)
	if err != nil {
		t.Fatalf("ListRecentTraces page 3: %v", err)
	}
	if len(traces) != 1 {
		t.Errorf("page 3 len = %d, want 1", len(traces))
	}
}

func TestTraceStore_Upsert_EmptyTraceID(t *testing.T) {
	db := setupTestDB(t)
	s := NewTraceStore(db)
	ctx := context.Background()

	entry := LogEntry{
		TraceID:   "",
		SpanID:    "span-1",
		Service:   "svc",
		Level:     "INFO",
		Timestamp: time.Now().UTC(),
	}

	// Should be a no-op
	if err := s.UpsertTraceStatus(ctx, "", entry); err != nil {
		t.Fatalf("UpsertTraceStatus empty: %v", err)
	}

	// No traces should exist
	_, total, err := s.ListRecentTraces(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListRecentTraces: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
}

func TestTraceStore_Upsert_FatalIsError(t *testing.T) {
	db := setupTestDB(t)
	s := NewTraceStore(db)
	ctx := context.Background()

	entry := LogEntry{
		TraceID:   "trace-fatal",
		SpanID:    "span-fatal",
		Service:   "svc",
		Level:     "FATAL",
		Timestamp: time.Now().UTC(),
	}

	if err := s.UpsertTraceStatus(ctx, entry.TraceID, entry); err != nil {
		t.Fatalf("UpsertTraceStatus: %v", err)
	}

	ts, _ := s.GetTraceStatus(ctx, "trace-fatal")
	if !ts.HasErrors {
		t.Error("HasErrors should be true for FATAL level")
	}
}
