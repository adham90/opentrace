package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
	"github.com/vmihailenco/msgpack/v5"
)

// ---------------------------------------------------------------------------
// Mocks: error grouping + impact tracking
// ---------------------------------------------------------------------------

type mockErrorGroupStore struct {
	mu       sync.Mutex
	upserted []store.LogEntry
}

func (m *mockErrorGroupStore) Upsert(_ context.Context, e store.LogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upserted = append(m.upserted, e)
	return nil
}
func (m *mockErrorGroupStore) Get(context.Context, string, string) (*store.ErrorGroup, error) {
	return nil, store.ErrNotFound
}
func (m *mockErrorGroupStore) List(context.Context, store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	return nil, nil
}
func (m *mockErrorGroupStore) Count(context.Context, store.ErrorGroupStatus, string) (int, error) {
	return 0, nil
}
func (m *mockErrorGroupStore) Resolve(context.Context, string, string, string) error { return nil }
func (m *mockErrorGroupStore) Ignore(context.Context, string, string, string) error  { return nil }
func (m *mockErrorGroupStore) Reopen(context.Context, string, string, string) error  { return nil }
func (m *mockErrorGroupStore) ListEvents(context.Context, string, string, int) ([]store.ErrorGroupEvent, error) {
	return nil, nil
}
func (m *mockErrorGroupStore) Prune(context.Context, time.Duration) (int64, error) { return 0, nil }

func (m *mockErrorGroupStore) snapshot() []store.LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.LogEntry(nil), m.upserted...)
}

type impactCall struct {
	fingerprint string
	environment string
	userID      string
	logID       int64
	service     string
}

type mockErrorImpactStore struct {
	mu    sync.Mutex
	calls []impactCall
}

func (m *mockErrorImpactStore) TrackImpact(_ context.Context, fp, env, userID string, _ map[string]any, logID int64, service string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, impactCall{fp, env, userID, logID, service})
	return nil
}
func (m *mockErrorImpactStore) GetImpact(context.Context, string, string) (*store.ErrorImpact, error) {
	return nil, store.ErrNotFound
}
func (m *mockErrorImpactStore) GetAffectedUsers(context.Context, string, int) ([]store.AffectedUser, error) {
	return nil, nil
}
func (m *mockErrorImpactStore) GetUserErrors(context.Context, string, time.Time) ([]store.ErrorSummary, error) {
	return nil, nil
}
func (m *mockErrorImpactStore) ComputeImpactScores(context.Context) error { return nil }
func (m *mockErrorImpactStore) TopByImpact(context.Context, store.ImpactQueryParams) ([]store.ErrorGroupWithImpact, error) {
	return nil, nil
}
func (m *mockErrorImpactStore) FindCommonTraits(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func (m *mockErrorImpactStore) snapshot() []impactCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]impactCall(nil), m.calls...)
}

// waitFor polls cond until it holds or the deadline passes. processAfterInsert
// runs in its own goroutine, so assertions on it must wait.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func post(t *testing.T, h *Handler, path, body, contentType string, flat bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	if flat {
		h.HandleFlatIngest(rec, req)
	} else {
		h.HandleIngestLogs(rec, req)
	}
	return rec
}

// ---------------------------------------------------------------------------
// Issue 1: the flat handler must not drop the SDK fields it parses
// ---------------------------------------------------------------------------

// TestFlatIngest_AllFieldsMapped asserts that every field the flat SDK sends and
// the columnar store has a column for reaches store.LogEntry. These were parsed
// into flatEntry and then silently dropped.
func TestFlatIngest_AllFieldsMapped(t *testing.T) {
	ls := &mockLogStore{}
	h := &Handler{LogStore: ls}

	body := `{
		"ts":"2026-04-04T12:41:00Z","level":"error","service":"billing","message":"boom",
		"env":"production","version":"abc123","host":"web-7","kind":"request",
		"event_type":"http.request","trace_id":"t1","span_id":"s1","parent_span_id":"p1",
		"request_id":"r1","user_id":"u1","tenant_id":"tn1","session_id":"sess1",
		"method":"POST","path":"/api/orders","route":"/api/orders/:id","handler":"OrdersController#create",
		"status":500,"duration_ms":1243,"db_ms":312,"db_count":8,
		"cache_ms":11,"cache_hits":4,"cache_misses":2,"ext_ms":77,"ext_count":3,
		"render_ms":45,"alloc_count":900,"mem_delta_mb":1.7,"n_plus_one":true,
		"slow_queries":2,"dup_queries":5,
		"error_class":"PayError","error_message":"card declined",
		"source_file":"app/pay.rb","source_line":87,
		"job_class":"SendEmailJob","job_queue":"mailers","job_id":"j-1","queue_ms":120
	}`

	rec := post(t, h, "/api/v2/logs", body, "application/json", true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(ls.insertedEntries) != 1 {
		t.Fatalf("inserted %d entries, want 1", len(ls.insertedEntries))
	}
	e := ls.insertedEntries[0]

	strs := map[string][2]string{
		"Service":        {e.Service, "billing"},
		"Environment":    {e.Environment, "production"},
		"CommitHash":     {e.CommitHash, "abc123"},
		"Host":           {e.Host, "web-7"},
		"Kind":           {e.Kind, "request"},
		"TraceID":        {e.TraceID, "t1"},
		"SpanID":         {e.SpanID, "s1"},
		"ParentSpanID":   {e.ParentSpanID, "p1"},
		"RequestID":      {e.RequestID, "r1"},
		"UserID":         {e.UserID, "u1"},
		"TenantID":       {e.TenantID, "tn1"},
		"SessionID":      {e.SessionID, "sess1"},
		"Route":          {e.Route, "/api/orders/:id"},
		"ExceptionClass": {e.ExceptionClass, "PayError"},
		"ErrorMessage":   {e.ErrorMessage, "card declined"},
		"SourceFile":     {e.SourceFile, "app/pay.rb"},
		"JobClass":       {e.JobClass, "SendEmailJob"},
		"JobQueue":       {e.JobQueue, "mailers"},
		"JobID":          {e.JobID, "j-1"},
		"EventType":      {e.EventType, "http.request"},
	}
	for name, v := range strs {
		if v[0] != v[1] {
			t.Errorf("%s = %q, want %q", name, v[0], v[1])
		}
	}

	ints := map[string][2]int{
		"CacheMs":     {e.CacheMs, 11},
		"CacheHits":   {e.CacheHits, 4},
		"CacheMisses": {e.CacheMisses, 2},
		"ExtMs":       {e.ExtMs, 77},
		"ExtCount":    {e.ExtCount, 3},
		"RenderMs":    {e.RenderMs, 45},
		"AllocCount":  {e.AllocCount, 900},
		// The wire carries megabytes; the column stores hundredths of a MB.
		"MemDeltaMb":  {e.MemDeltaMb, 170},
		"SlowQueries": {e.SlowQueries, 2},
		"SourceLine":  {e.SourceLine, 87},
		"QueueMs":     {e.QueueMs, 120},
	}
	for name, v := range ints {
		if v[0] != v[1] {
			t.Errorf("%s = %d, want %d", name, v[0], v[1])
		}
	}

	if e.RequestSummary == nil {
		t.Fatal("RequestSummary is nil for an http.request entry")
	}
	rs := e.RequestSummary
	if rs.Method != "POST" || rs.Path != "/api/orders" || rs.Status != 500 ||
		rs.DurationMs != 1243 || rs.DBTimeMs != 312 || rs.SQLCount != 8 ||
		!rs.NPlusOne || rs.DuplicateQueries != 5 || rs.Controller != "OrdersController#create" {
		t.Errorf("RequestSummary mismapped: %+v", rs)
	}
}

// ---------------------------------------------------------------------------
// Issue 2: /api/logs must store user_id and fire impact tracking
// ---------------------------------------------------------------------------

func TestIngestLogs_StoresUserIDAndTracksImpact(t *testing.T) {
	ls := &mockLogStore{}
	eg := &mockErrorGroupStore{}
	ei := &mockErrorImpactStore{}
	h := &Handler{LogStore: ls, ErrorGroupStore: eg, ErrorImpactStore: ei}

	body := `{"level":"error","message":"boom","service":"api","env":"production",
		"error_class":"PayError","user_id":"42","source_file":"app/pay.rb"}`

	rec := post(t, h, "/api/logs", body, "application/json", false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(ls.insertedEntries) != 1 {
		t.Fatalf("inserted %d entries, want 1", len(ls.insertedEntries))
	}
	if got := ls.insertedEntries[0].UserID; got != "42" {
		t.Fatalf("stored UserID = %q, want %q", got, "42")
	}
	if ls.insertedEntries[0].ErrorFingerprint == "" {
		t.Fatal("no fingerprint computed for an error entry")
	}

	waitFor(t, "TrackImpact", func() bool { return len(ei.snapshot()) == 1 })
	call := ei.snapshot()[0]
	if call.userID != "42" {
		t.Errorf("TrackImpact userID = %q, want %q", call.userID, "42")
	}
	if call.environment != "production" || call.service != "api" {
		t.Errorf("TrackImpact env/service = %q/%q", call.environment, call.service)
	}
	if len(eg.snapshot()) != 1 {
		t.Errorf("error group upserts = %d, want 1", len(eg.snapshot()))
	}
}

// TestIngestLogs_MapsPreviouslyDroppedFields covers the nested handler's share
// of issue 1: tenant/session/error_message/slow_queries/dup_queries were parsed
// and then thrown away.
func TestIngestLogs_MapsPreviouslyDroppedFields(t *testing.T) {
	ls := &mockLogStore{}
	h := &Handler{LogStore: ls}

	body := `{"level":"info","message":"req","service":"api","tenant_id":"tn9","session_id":"s9",
		"error_message":"soft failure","slow_queries":3,"dup_queries":7,
		"method":"GET","path":"/x","status":200,"duration_ms":10,"host":"web-1"}`

	rec := post(t, h, "/api/logs", body, "application/json", false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	e := ls.insertedEntries[0]
	if e.TenantID != "tn9" || e.SessionID != "s9" || e.ErrorMessage != "soft failure" ||
		e.SlowQueries != 3 || e.Host != "web-1" {
		t.Errorf("nested handler dropped fields: %+v", e)
	}
	if e.RequestSummary == nil || e.RequestSummary.DuplicateQueries != 7 {
		t.Errorf("DuplicateQueries not mapped: %+v", e.RequestSummary)
	}
}

// ---------------------------------------------------------------------------
// Issue 4: both endpoints validate level identically
// ---------------------------------------------------------------------------

func TestBothEndpoints_RejectUnknownLevel(t *testing.T) {
	for _, tc := range []struct{ name, level string }{
		{"critical", "critical"}, {"trace", "trace"}, {"notice", "notice"}, {"err", "err"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"level":"` + tc.level + `","message":"m","service":"api"}`

			flat := post(t, &Handler{LogStore: &mockLogStore{}}, "/api/v2/logs", body, "application/json", true)
			if flat.Code != http.StatusBadRequest {
				t.Errorf("flat: status = %d, want 400", flat.Code)
			}
			nested := post(t, &Handler{LogStore: &mockLogStore{}}, "/api/logs", body, "application/json", false)
			if nested.Code != http.StatusBadRequest {
				t.Errorf("nested: status = %d, want 400", nested.Code)
			}
		})
	}
}

func TestBothEndpoints_AcceptWarningAlias(t *testing.T) {
	for _, flat := range []bool{true, false} {
		ls := &mockLogStore{}
		rec := post(t, &Handler{LogStore: ls}, "/api/logs", `{"level":"WARNING","message":"m"}`, "application/json", flat)
		if rec.Code != http.StatusCreated {
			t.Fatalf("flat=%v: status = %d, want 201: %s", flat, rec.Code, rec.Body.String())
		}
		if ls.insertedEntries[0].Level != "warn" {
			t.Errorf("flat=%v: level = %q, want warn", flat, ls.insertedEntries[0].Level)
		}
	}
}

// ---------------------------------------------------------------------------
// Issue 5: duration alone must not classify an entry as an HTTP request
// ---------------------------------------------------------------------------

func TestKindClassification(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantKind       string
		wantReqSummary bool
	}{
		{
			name:           "job with duration is not a request",
			body:           `{"level":"info","message":"SendEmailJob done","event_type":"job.perform","job_class":"SendEmailJob","duration_ms":850}`,
			wantKind:       kindJob,
			wantReqSummary: false,
		},
		{
			name:           "explicit kind wins",
			body:           `{"level":"info","message":"m","kind":"job","duration_ms":850,"method":"GET"}`,
			wantKind:       kindJob,
			wantReqSummary: false,
		},
		{
			name:           "timed generic event is not a request",
			body:           `{"level":"info","message":"m","event_type":"cache.warm","duration_ms":300}`,
			wantKind:       kindEvent,
			wantReqSummary: false,
		},
		{
			name:           "plain timed log is not a request",
			body:           `{"level":"info","message":"m","duration_ms":300}`,
			wantKind:       kindLog,
			wantReqSummary: false,
		},
		{
			name:           "http.request event_type is a request",
			body:           `{"level":"info","message":"m","event_type":"http.request","duration_ms":12}`,
			wantKind:       kindRequest,
			wantReqSummary: true,
		},
		{
			name:           "method+path is a request",
			body:           `{"level":"info","message":"m","method":"GET","path":"/x","duration_ms":12}`,
			wantKind:       kindRequest,
			wantReqSummary: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := &mockLogStore{}
			rec := post(t, &Handler{LogStore: ls}, "/api/v2/logs", tt.body, "application/json", true)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			e := ls.insertedEntries[0]
			if e.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", e.Kind, tt.wantKind)
			}
			if (e.RequestSummary != nil) != tt.wantReqSummary {
				t.Errorf("RequestSummary present = %v, want %v", e.RequestSummary != nil, tt.wantReqSummary)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Issue 6: /api/logs must tolerate fractional numbers (JSON and msgpack)
// ---------------------------------------------------------------------------

func TestIngestLogs_FractionalNumbersDoNotRejectBatch(t *testing.T) {
	ls := &mockLogStore{}
	h := &Handler{LogStore: ls}

	body := `[
		{"level":"info","message":"req","method":"GET","path":"/x","duration_ms":12.35,"db_ms":3.14,"db_count":2},
		{"level":"info","message":"plain"}
	]`
	rec := post(t, h, "/api/logs", body, "application/json", false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("fractional value rejected the batch: %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["count"].(float64) != 2 {
		t.Fatalf("count = %v, want 2", resp["count"])
	}
	if got := ls.insertedEntries[0].RequestSummary.DurationMs; got != 12 {
		t.Errorf("duration_ms = %v, want 12 (rounded)", got)
	}
}

func TestIngestLogs_MsgpackFractionalNumbers(t *testing.T) {
	ls := &mockLogStore{}
	h := &Handler{LogStore: ls}

	payload := []map[string]any{
		{"level": "info", "message": "req", "method": "GET", "path": "/x", "duration_ms": 12.35, "db_count": 2},
	}
	raw, err := msgpack.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/logs", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/msgpack")
	rec := httptest.NewRecorder()
	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("msgpack fractional rejected: %d %s", rec.Code, rec.Body.String())
	}
	if got := ls.insertedEntries[0].RequestSummary.DurationMs; got != 12 {
		t.Errorf("duration_ms = %v, want 12", got)
	}
}

// ---------------------------------------------------------------------------
// Issue 7: post-insert side effects see the IDs the store assigned
// ---------------------------------------------------------------------------

// TestProcessAfterInsert_PropagatesAssignedLogIDs pins the ingest side of the
// last_log_id linkage: whatever IDs BatchInsert writes back into the slice must
// reach ErrorGroupStore.Upsert and ErrorImpactStore.TrackImpact.
func TestProcessAfterInsert_PropagatesAssignedLogIDs(t *testing.T) {
	ls := &mockLogStore{}
	ls.batchInsertFn = func(_ context.Context, entries []store.LogEntry) (int, error) {
		for i := range entries {
			entries[i].ID = int64(1000 + i)
		}
		return len(entries), nil
	}
	eg := &mockErrorGroupStore{}
	ei := &mockErrorImpactStore{}
	h := &Handler{LogStore: ls, ErrorGroupStore: eg, ErrorImpactStore: ei}

	body := `{"level":"error","message":"boom","service":"api","error_class":"E","user_id":"u1"}`
	rec := post(t, h, "/api/logs", body, "application/json", false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}

	waitFor(t, "TrackImpact", func() bool { return len(ei.snapshot()) == 1 })
	if got := ei.snapshot()[0].logID; got != 1000 {
		t.Errorf("TrackImpact logID = %d, want 1000 (store-assigned ID lost)", got)
	}
	groups := eg.snapshot()
	if len(groups) != 1 || groups[0].ID != 1000 {
		t.Errorf("Upsert entry ID = %v, want 1000", groups)
	}
}

// ---------------------------------------------------------------------------
// Field-length caps at the boundary
// ---------------------------------------------------------------------------

func TestTruncateField(t *testing.T) {
	if got := truncateField("short", 100); got != "short" {
		t.Errorf("unchanged value mangled: %q", got)
	}
	long := strings.Repeat("a", 1000)
	got := truncateField(long, 100)
	if len(got) > 100 {
		t.Errorf("len = %d, want <= 100", len(got))
	}
	if !strings.HasSuffix(got, truncationMarker) {
		t.Errorf("missing truncation marker: %q", got)
	}
	// Multi-byte input must stay valid UTF-8 after the cut.
	multi := strings.Repeat("é", 200)
	cut := truncateField(multi, 51)
	if len(cut) > 51 {
		t.Errorf("len = %d, want <= 51", len(cut))
	}
	for _, r := range cut {
		if r == '�' {
			t.Fatalf("truncation produced invalid UTF-8: %q", cut)
		}
	}
}

// TestFlatIngest_CapsOversizedFields guards the WAL string encoder, which
// mis-frames a segment when any single field exceeds 65535 bytes.
func TestFlatIngest_CapsOversizedFields(t *testing.T) {
	ls := &mockLogStore{}
	h := &Handler{LogStore: ls}

	huge := strings.Repeat("x", 200000)
	body, _ := json.Marshal(map[string]any{
		"level": "error", "message": huge, "service": huge, "trace_id": huge,
		"path": huge, "error_message": huge, "source_file": huge, "job_class": huge,
	})

	rec := post(t, h, "/api/v2/logs", string(body), "application/json", true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	e := ls.insertedEntries[0]
	checks := map[string]struct {
		v   string
		max int
	}{
		"Message":      {e.Message, maxMessageBytes},
		"ErrorMessage": {e.ErrorMessage, maxMessageBytes},
		"Service":      {e.Service, maxIdentFieldBytes},
		"TraceID":      {e.TraceID, maxIdentFieldBytes},
		"JobClass":     {e.JobClass, maxIdentFieldBytes},
		"SourceFile":   {e.SourceFile, maxPathFieldBytes},
	}
	for name, c := range checks {
		if len(c.v) > c.max {
			t.Errorf("%s len = %d, want <= %d", name, len(c.v), c.max)
		}
		if len(c.v) >= 65535 {
			t.Errorf("%s exceeds the WAL 16-bit length limit", name)
		}
	}
	if e.RequestSummary != nil && len(e.RequestSummary.Path) > maxPathFieldBytes {
		t.Errorf("Path len = %d, want <= %d", len(e.RequestSummary.Path), maxPathFieldBytes)
	}
}

// ---------------------------------------------------------------------------
// Issues 10 & 11: queue durability
// ---------------------------------------------------------------------------

// TestEnqueue_OverflowIsAllOrNothing asserts that a batch which does not fit is
// never split between the buffer and a synchronous insert: on insert failure
// nothing may already be buffered, otherwise a client retry duplicates rows.
func TestEnqueue_OverflowIsAllOrNothing(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{MaxQueueSize: 4, MaxBatchSize: 100, FlushInterval: time.Hour})
	defer q.Stop()

	// Fill 3 of 4 slots.
	if _, err := q.Enqueue(context.Background(), makeEntries(3)); err != nil {
		t.Fatalf("fill: %v", err)
	}
	// Now make the store fail and push a batch that cannot fit.
	ms.mu.Lock()
	ms.batchErr = errors.New("SQLITE_BUSY")
	ms.mu.Unlock()

	batch := makeEntries(10)
	_, err := q.Enqueue(context.Background(), batch)
	if err == nil {
		t.Fatal("expected the sync-insert error to surface")
	}
	// The failing batch must not be partially buffered: the only thing that may
	// remain is the earlier flush's re-queued content, never a prefix of `batch`.
	if depth := q.QueueDepth(); depth > 3 {
		t.Fatalf("queue depth = %d: part of the failed batch was buffered", depth)
	}
}

// TestFlushBatch_RequeuesOnFailure asserts a failed flush is retried rather
// than silently discarded.
func TestFlushBatch_RequeuesOnFailure(t *testing.T) {
	ms := &queueMockLogStore{batchErr: errors.New("disk full")}
	q := NewQueue(ms, QueueConfig{MaxQueueSize: 100, MaxBatchSize: 100, FlushInterval: time.Hour})

	if _, err := q.Enqueue(context.Background(), makeEntries(5)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	q.Flush()

	if got := q.FlushCount(); got != flushMaxAttempts {
		t.Errorf("flush attempts = %d, want %d", got, flushMaxAttempts)
	}
	if depth := q.QueueDepth(); depth != 5 {
		t.Fatalf("queue depth after failed flush = %d, want 5 (entries must be re-queued)", depth)
	}
	if q.DropCount() != 0 {
		t.Errorf("drop count = %d, want 0", q.DropCount())
	}

	// Recover: the re-queued entries must land on the next flush.
	ms.mu.Lock()
	ms.batchErr = nil
	ms.mu.Unlock()
	q.Flush()

	if ms.totalInserted() < 5 {
		t.Errorf("re-queued entries never persisted (inserted %d)", ms.totalInserted())
	}
	if q.QueueDepth() != 0 {
		t.Errorf("queue depth = %d, want 0", q.QueueDepth())
	}
	q.Stop()
}

// TestStoreAndRespond_BatchIDBypassesQueue asserts a batch carrying an
// X-Batch-ID is written synchronously, so RecordBatch (which permanently
// suppresses the SDK's retry) only ever follows a durable write.
func TestStoreAndRespond_BatchIDBypassesQueue(t *testing.T) {
	ls := &mockLogStore{}
	q := NewQueue(&queueMockLogStore{}, QueueConfig{MaxQueueSize: 100, MaxBatchSize: 100, FlushInterval: time.Hour})
	defer q.Stop()
	h := &Handler{LogStore: ls, Queue: q}

	req := httptest.NewRequest("POST", "/api/logs", bytes.NewBufferString(`{"level":"info","message":"m"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Batch-ID", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	rec := httptest.NewRecorder()
	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(ls.insertedEntries) != 1 {
		t.Fatalf("entry was buffered instead of written durably (inserted %d)", len(ls.insertedEntries))
	}
	if len(ls.recordedBatches) != 1 {
		t.Fatalf("batch not recorded")
	}
	if q.QueueDepth() != 0 {
		t.Errorf("queue depth = %d, want 0", q.QueueDepth())
	}
}

// TestStoreAndRespond_NoBatchIDUsesQueue keeps the async path alive for
// un-batched traffic.
func TestStoreAndRespond_NoBatchIDUsesQueue(t *testing.T) {
	ms := &queueMockLogStore{}
	q := NewQueue(ms, QueueConfig{MaxQueueSize: 100, MaxBatchSize: 100, FlushInterval: time.Hour})
	defer q.Stop()
	h := &Handler{LogStore: &mockLogStore{}, Queue: q}

	rec := post(t, h, "/api/logs", `{"level":"info","message":"m"}`, "application/json", false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if q.QueueDepth() != 1 {
		t.Errorf("queue depth = %d, want 1", q.QueueDepth())
	}
}
