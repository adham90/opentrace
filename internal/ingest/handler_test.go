package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Minimal mock stores for handler tests
// ---------------------------------------------------------------------------

// mockLogStore implements the subset of store.LogStore used by HandleIngestLogs.
type mockLogStore struct {
	batchInsertFn func(ctx context.Context, entries []store.LogEntry) (int, error)
	recordBatchFn func(ctx context.Context, batchID string, logCount int) error
	getBatchFn    func(ctx context.Context, batchID string) (*store.BatchRecord, error)

	// Records of calls for assertions
	insertedEntries []store.LogEntry
	recordedBatches []string
}

func (m *mockLogStore) BatchInsert(ctx context.Context, entries []store.LogEntry) (int, error) {
	m.insertedEntries = append(m.insertedEntries, entries...)
	if m.batchInsertFn != nil {
		return m.batchInsertFn(ctx, entries)
	}
	return len(entries), nil
}

func (m *mockLogStore) RecordBatch(ctx context.Context, batchID string, logCount int) error {
	m.recordedBatches = append(m.recordedBatches, batchID)
	if m.recordBatchFn != nil {
		return m.recordBatchFn(ctx, batchID, logCount)
	}
	return nil
}

func (m *mockLogStore) GetBatch(ctx context.Context, batchID string) (*store.BatchRecord, error) {
	if m.getBatchFn != nil {
		return m.getBatchFn(ctx, batchID)
	}
	return nil, store.ErrNotFound
}

// Unused interface methods — satisfy the full LogStore interface.
func (m *mockLogStore) Search(context.Context, store.LogSearchParams) ([]store.LogEntry, error) {
	return nil, nil
}
func (m *mockLogStore) Prune(context.Context, time.Duration) (int64, error) { return 0, nil }
func (m *mockLogStore) CountByLevel(context.Context, store.LogCountParams) (map[string]int, error) {
	return nil, nil
}
func (m *mockLogStore) CountByService(context.Context, store.LogCountParams) ([]store.ServiceLogCount, error) {
	return nil, nil
}
func (m *mockLogStore) Histogram(context.Context, store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	return nil, nil
}
func (m *mockLogStore) DistinctValues(context.Context, string, store.LogCountParams) ([]string, error) {
	return nil, nil
}
func (m *mockLogStore) MetadataKeys(context.Context, store.LogCountParams) ([]string, error) {
	return nil, nil
}
func (m *mockLogStore) GetByID(context.Context, int64) (*store.LogEntry, error) { return nil, nil }
func (m *mockLogStore) SearchRequestSummaries(context.Context, store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	return nil, nil
}
func (m *mockLogStore) AggregateRequestSummaries(context.Context, store.RequestSummaryAggregateParams) (*store.RequestSummaryAggregates, error) {
	return &store.RequestSummaryAggregates{}, nil
}
func (m *mockLogStore) PruneBatches(context.Context, time.Duration) (int64, error) { return 0, nil }

// mockSettingsStore implements the subset of store.SettingsStore needed by the handler.
type mockSettingsStore struct {
	samplingRules []store.SamplingRule
	samplingErr   error
}

func (m *mockSettingsStore) GetSamplingRules(context.Context) ([]store.SamplingRule, error) {
	return m.samplingRules, m.samplingErr
}

// Unused interface methods.
func (m *mockSettingsStore) GetRetention(context.Context) (*store.RetentionSettings, error) {
	return nil, nil
}
func (m *mockSettingsStore) SetRetention(context.Context, store.RetentionSettings) error { return nil }
func (m *mockSettingsStore) GetAPIKey(context.Context) (string, error)                   { return "", nil }
func (m *mockSettingsStore) SetAPIKey(context.Context, string) error                     { return nil }
func (m *mockSettingsStore) GetCORSOrigins(context.Context) (string, error)              { return "", nil }
func (m *mockSettingsStore) SetCORSOrigins(context.Context, string) error                { return nil }
func (m *mockSettingsStore) GetMaxQueryRows(context.Context) (int, error)                { return 0, nil }
func (m *mockSettingsStore) SetMaxQueryRows(context.Context, int) error                  { return nil }
func (m *mockSettingsStore) GetStatementTimeout(context.Context) (int, error)            { return 0, nil }
func (m *mockSettingsStore) SetStatementTimeout(context.Context, int) error              { return nil }
func (m *mockSettingsStore) GetMCPName(context.Context) (string, error)                  { return "", nil }
func (m *mockSettingsStore) SetMCPName(context.Context, string) error                    { return nil }
func (m *mockSettingsStore) SetSamplingRules(context.Context, []store.SamplingRule) error { return nil }
func (m *mockSettingsStore) GetTelegramConfig(context.Context) (*store.TelegramConfig, error) {
	return &store.TelegramConfig{}, nil
}
func (m *mockSettingsStore) SetTelegramConfig(context.Context, store.TelegramConfig) error {
	return nil
}

// mockWatchStream satisfies WatchStreamEvaluator.
type mockWatchStream struct {
	received []store.LogEntry
}

func (m *mockWatchStream) OnLogsReceived(entries []store.LogEntry) {
	m.received = append(m.received, entries...)
}

// ---------------------------------------------------------------------------
// Helper: build a minimal Handler wired to mock stores
// ---------------------------------------------------------------------------

func newTestHandler(logStore *mockLogStore) *Handler {
	if logStore == nil {
		logStore = &mockLogStore{}
	}
	return &Handler{
		LogStore:      logStore,
		SettingsStore: &mockSettingsStore{},
		DSStore:       &mockDSStore{},
		Registry:      connector.NewRegistry(),
		Cfg:           &config.Config{},
	}
}

// mockDSStore is a minimal DataSourceStore for handler tests that avoids
// uuid.UUID type issues by implementing the interface directly.
type mockDSStore struct{}

func (m *mockDSStore) Create(ctx context.Context, p store.CreateDataSourceParams) (*store.DataSource, error) {
	return &store.DataSource{Name: p.Name, Type: p.Type}, nil
}

func (m *mockDSStore) GetByID(ctx context.Context, id uuid.UUID) (*store.DataSource, error) {
	return nil, store.ErrNotFound
}

func (m *mockDSStore) List(ctx context.Context, p store.ListDataSourceParams) ([]store.DataSource, error) {
	return nil, nil
}

func (m *mockDSStore) Update(ctx context.Context, id uuid.UUID, p store.UpdateDataSourceParams) (*store.DataSource, error) {
	return &store.DataSource{}, nil
}

func (m *mockDSStore) Delete(ctx context.Context, id uuid.UUID) error { return nil }

// Compile-time check: mockDSStore must satisfy store.DataSourceStore.
var _ store.DataSourceStore = (*mockDSStore)(nil)

// makeRequest builds an httptest request with the given JSON body and optional headers.
func makeRequest(t *testing.T, body any, headers map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encoding request body: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/logs", &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// decodeResponse decodes JSON response body into a map.
func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response body: %v\nbody: %s", err, rec.Body.String())
	}
	return resp
}

// validLogEntry returns a minimal valid ingestLogEntry-shaped map.
func validLogEntry() map[string]any {
	return map[string]any{
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
		"level":   "info",
		"message": "test log message",
		"service": "test-svc",
	}
}

// ---------------------------------------------------------------------------
// Tests: IsValidBatchID
// ---------------------------------------------------------------------------

func TestIsValidBatchID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{
			name: "valid UUID lowercase",
			id:   "550e8400-e29b-41d4-a716-446655440000",
			want: true,
		},
		{
			name: "valid UUID uppercase",
			id:   "550E8400-E29B-41D4-A716-446655440000",
			want: true,
		},
		{
			name: "valid UUID mixed case",
			id:   "550e8400-E29B-41d4-a716-446655440000",
			want: true,
		},
		{
			name: "empty string",
			id:   "",
			want: false,
		},
		{
			name: "too short",
			id:   "550e8400-e29b",
			want: false,
		},
		{
			name: "missing dashes",
			id:   "550e8400e29b41d4a716446655440000",
			want: false,
		},
		{
			name: "non-hex characters",
			id:   "550e8400-e29b-41d4-a716-44665544ZZZZ",
			want: false,
		},
		{
			name: "extra characters appended",
			id:   "550e8400-e29b-41d4-a716-446655440000extra",
			want: false,
		},
		{
			name: "extra characters prepended",
			id:   "xx550e8400-e29b-41d4-a716-446655440000",
			want: false,
		},
		{
			name: "dashes in wrong positions",
			id:   "550e84-00e29b-41d4-a716-4466554400-00",
			want: false,
		},
		{
			name: "single character",
			id:   "a",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidBatchID(tt.id)
			if got != tt.want {
				t.Errorf("IsValidBatchID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: HandleIngestLogs
// ---------------------------------------------------------------------------

func TestHandleIngestLogs_ValidSingleEntry(t *testing.T) {
	logStore := &mockLogStore{}
	h := newTestHandler(logStore)

	entry := validLogEntry()
	req := makeRequest(t, entry, nil)
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	count, ok := resp["count"].(float64)
	if !ok || count != 1 {
		t.Errorf("expected count=1, got %v", resp["count"])
	}
	if len(logStore.insertedEntries) != 1 {
		t.Errorf("expected 1 inserted entry, got %d", len(logStore.insertedEntries))
	}
}

func TestHandleIngestLogs_ValidBatch(t *testing.T) {
	logStore := &mockLogStore{}
	h := newTestHandler(logStore)

	batch := []map[string]any{
		validLogEntry(),
		validLogEntry(),
		validLogEntry(),
	}
	// Set different messages for variety
	batch[0]["message"] = "first"
	batch[1]["message"] = "second"
	batch[2]["message"] = "third"

	req := makeRequest(t, batch, nil)
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	count, ok := resp["count"].(float64)
	if !ok || count != 3 {
		t.Errorf("expected count=3, got %v", resp["count"])
	}
	if len(logStore.insertedEntries) != 3 {
		t.Errorf("expected 3 inserted entries, got %d", len(logStore.insertedEntries))
	}
}

func TestHandleIngestLogs_InvalidJSON(t *testing.T) {
	h := newTestHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(`{invalid json`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if _, ok := resp["error"]; !ok {
		t.Error("expected error field in response")
	}
}

func TestHandleIngestLogs_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name       string
		entry      map[string]any
		wantFields []string // substrings expected in the error
	}{
		{
			name: "missing level",
			entry: map[string]any{
				"ts":      time.Now().UTC().Format(time.RFC3339),
				"message": "hello",
			},
			wantFields: []string{"level"},
		},
		{
			name: "missing message",
			entry: map[string]any{
				"ts":    time.Now().UTC().Format(time.RFC3339),
				"level": "info",
			},
			wantFields: []string{"message"},
		},
		{
			name:       "missing all required fields",
			entry:      map[string]any{},
			wantFields: []string{"level", "message"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler(nil)
			req := makeRequest(t, tt.entry, nil)
			rec := httptest.NewRecorder()

			h.HandleIngestLogs(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
			}
			resp := decodeResponse(t, rec)
			errMsg, _ := resp["error"].(string)
			for _, field := range tt.wantFields {
				if !bytes.Contains([]byte(errMsg), []byte(field)) {
					t.Errorf("expected error to mention %q, got: %s", field, errMsg)
				}
			}
		})
	}
}

func TestHandleIngestLogs_InvalidLogLevel(t *testing.T) {
	h := newTestHandler(nil)

	entry := validLogEntry()
	entry["level"] = "TRACE" // not a valid level
	req := makeRequest(t, entry, nil)
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	errMsg, _ := resp["error"].(string)
	if !bytes.Contains([]byte(errMsg), []byte("level")) {
		t.Errorf("expected error to mention 'level', got: %s", errMsg)
	}
}

func TestHandleIngestLogs_InvalidAPIVersion(t *testing.T) {
	h := newTestHandler(nil)

	entry := validLogEntry()
	req := makeRequest(t, entry, map[string]string{"X-API-Version": "notanumber"})
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	errMsg, _ := resp["error"].(string)
	if !bytes.Contains([]byte(errMsg), []byte("X-API-Version")) {
		t.Errorf("expected error to mention 'X-API-Version', got: %s", errMsg)
	}
	// Server should set the response header with its API version
	if rec.Header().Get("X-API-Version") == "" {
		t.Error("expected X-API-Version response header to be set")
	}
}

func TestHandleIngestLogs_BelowMinAPIVersion(t *testing.T) {
	h := newTestHandler(nil)

	entry := validLogEntry()
	req := makeRequest(t, entry, map[string]string{"X-API-Version": "0"})
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	errMsg, _ := resp["error"].(string)
	if !bytes.Contains([]byte(errMsg), []byte("below minimum")) {
		t.Errorf("expected error to mention 'below minimum', got: %s", errMsg)
	}
	if rec.Header().Get("X-Min-Client-API-Version") == "" {
		t.Error("expected X-Min-Client-API-Version response header to be set")
	}
}

func TestHandleIngestLogs_ValidAPIVersion(t *testing.T) {
	h := newTestHandler(nil)

	entry := validLogEntry()
	req := makeRequest(t, entry, map[string]string{"X-API-Version": fmt.Sprintf("%d", APIVersion)})
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 with valid API version, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleIngestLogs_DuplicateBatch(t *testing.T) {
	batchID := "550e8400-e29b-41d4-a716-446655440000"
	logStore := &mockLogStore{
		getBatchFn: func(_ context.Context, id string) (*store.BatchRecord, error) {
			if id == batchID {
				return &store.BatchRecord{
					BatchID:    batchID,
					LogCount:   5,
					ReceivedAt: time.Now(),
				}, nil
			}
			return nil, store.ErrNotFound
		},
	}
	h := newTestHandler(logStore)

	entry := validLogEntry()
	req := makeRequest(t, entry, map[string]string{"X-Batch-ID": batchID})
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 for duplicate batch, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if dedup, ok := resp["deduplicated"].(bool); !ok || !dedup {
		t.Errorf("expected deduplicated=true, got %v", resp["deduplicated"])
	}
	count, ok := resp["count"].(float64)
	if !ok || count != 5 {
		t.Errorf("expected count=5 (from original batch), got %v", resp["count"])
	}
	// Should NOT have called BatchInsert
	if len(logStore.insertedEntries) > 0 {
		t.Error("did not expect any entries to be inserted for a duplicate batch")
	}
}

func TestHandleIngestLogs_InvalidBatchID(t *testing.T) {
	h := newTestHandler(nil)

	entry := validLogEntry()
	req := makeRequest(t, entry, map[string]string{"X-Batch-ID": "not-a-uuid"})
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for invalid batch ID, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	errMsg, _ := resp["error"].(string)
	if !bytes.Contains([]byte(errMsg), []byte("X-Batch-ID")) {
		t.Errorf("expected error to mention 'X-Batch-ID', got: %s", errMsg)
	}
}

func TestHandleIngestLogs_EmptyBody(t *testing.T) {
	h := newTestHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(""))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for empty body, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleIngestLogs_StoreError(t *testing.T) {
	logStore := &mockLogStore{
		batchInsertFn: func(_ context.Context, _ []store.LogEntry) (int, error) {
			return 0, errors.New("database is locked")
		},
	}
	h := newTestHandler(logStore)

	entry := validLogEntry()
	req := makeRequest(t, entry, nil)
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 on store error, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	errMsg, _ := resp["error"].(string)
	if !bytes.Contains([]byte(errMsg), []byte("failed to insert")) {
		t.Errorf("expected error to mention 'failed to insert', got: %s", errMsg)
	}
}

func TestHandleIngestLogs_NewBatchRecorded(t *testing.T) {
	batchID := "550e8400-e29b-41d4-a716-446655440000"
	logStore := &mockLogStore{}
	h := newTestHandler(logStore)

	entry := validLogEntry()
	req := makeRequest(t, entry, map[string]string{"X-Batch-ID": batchID})
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(logStore.recordedBatches) != 1 || logStore.recordedBatches[0] != batchID {
		t.Errorf("expected batch %q to be recorded, got %v", batchID, logStore.recordedBatches)
	}
}

func TestHandleIngestLogs_AllValidLevels(t *testing.T) {
	levels := []string{
		"debug", "info", "warn", "warning", "error", "fatal",
		"DEBUG", "INFO", "WARN", "WARNING", "ERROR", "FATAL",
	}
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			h := newTestHandler(nil)
			entry := validLogEntry()
			entry["level"] = level
			req := makeRequest(t, entry, nil)
			rec := httptest.NewRecorder()

			h.HandleIngestLogs(rec, req)

			if rec.Code != http.StatusCreated {
				t.Errorf("expected status 201 for level %q, got %d: %s", level, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleIngestLogs_WithBody(t *testing.T) {
	logStore := &mockLogStore{}
	h := newTestHandler(logStore)

	entry := validLogEntry()
	entry["body"] = map[string]any{
		"context": map[string]any{
			"user_id":    "u-123",
			"request_ms": 42.5,
		},
	}
	req := makeRequest(t, entry, nil)
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(logStore.insertedEntries) != 1 {
		t.Fatal("expected 1 inserted entry")
	}
	inserted := logStore.insertedEntries[0]
	if inserted.MetadataJSON == "" {
		t.Error("expected MetadataJSON to be set from body")
	}
	if inserted.Metadata == nil {
		t.Error("expected Metadata map to be parsed from body")
	}
}

func TestHandleIngestLogs_SampledResponse(t *testing.T) {
	logStore := &mockLogStore{}
	h := newTestHandler(logStore)
	// Set sampling rules that drop everything for the target service
	h.SettingsStore = &mockSettingsStore{
		samplingRules: []store.SamplingRule{
			{Service: "test-svc", Rate: 0.0, KeepErrors: false},
		},
	}

	entry := validLogEntry()
	entry["service"] = "test-svc"
	entry["level"] = "info"
	req := makeRequest(t, entry, nil)
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	// When all entries are sampled away, count=0 and status is 200
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 when all sampled away, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if sampled, ok := resp["sampled"].(bool); !ok || !sampled {
		t.Errorf("expected sampled=true in response, got %v", resp["sampled"])
	}
}

func TestHandleIngestLogs_NoBatchIDSkipsDedup(t *testing.T) {
	logStore := &mockLogStore{}
	h := newTestHandler(logStore)

	entry := validLogEntry()
	// No X-Batch-ID header
	req := makeRequest(t, entry, nil)
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	// RecordBatch should NOT have been called
	if len(logStore.recordedBatches) != 0 {
		t.Errorf("expected no batches recorded without X-Batch-ID, got %d", len(logStore.recordedBatches))
	}
}

func TestHandleIngestLogs_ArrayOfInvalidJSON(t *testing.T) {
	h := newTestHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(`[{bad}]`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for invalid JSON array, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleIngestLogs_EmptyArray(t *testing.T) {
	h := newTestHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(`[]`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	// Empty array passes validation (no entries to check), but count=0 => 200
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 for empty array, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	count, _ := resp["count"].(float64)
	if count != 0 {
		t.Errorf("expected count=0 for empty array, got %v", count)
	}
}

// ---------------------------------------------------------------------------
// Tests: MessagePack ingest
// ---------------------------------------------------------------------------

// makeMsgpackRequest builds an httptest request with a MessagePack-encoded body.
func makeMsgpackRequest(t *testing.T, body any, headers map[string]string) *http.Request {
	t.Helper()
	data, err := msgpack.Marshal(body)
	if err != nil {
		t.Fatalf("encoding msgpack body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/msgpack")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestHandleIngestLogs_MsgpackSingleEntry(t *testing.T) {
	logStore := &mockLogStore{}
	h := newTestHandler(logStore)

	entry := ingestLogEntry{
		Ts: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339Nano),
		Level:     "info",
		Message:   "msgpack single entry",
		Service:   "test-svc",
	}
	req := makeMsgpackRequest(t, entry, nil)
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	count, ok := resp["count"].(float64)
	if !ok || count != 1 {
		t.Errorf("expected count=1, got %v", resp["count"])
	}
	if len(logStore.insertedEntries) != 1 {
		t.Fatalf("expected 1 inserted entry, got %d", len(logStore.insertedEntries))
	}
	if logStore.insertedEntries[0].Message != "msgpack single entry" {
		t.Errorf("expected message 'msgpack single entry', got %q", logStore.insertedEntries[0].Message)
	}
}

func TestHandleIngestLogs_MsgpackBatch(t *testing.T) {
	logStore := &mockLogStore{}
	h := newTestHandler(logStore)

	batch := []ingestLogEntry{
		{
			Ts: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339Nano),
			Level:     "info",
			Message:   "first msgpack",
			Service:   "test-svc",
		},
		{
			Ts: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339Nano),
			Level:     "warn",
			Message:   "second msgpack",
			Service:   "test-svc",
		},
	}
	req := makeMsgpackRequest(t, batch, nil)
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	count, ok := resp["count"].(float64)
	if !ok || count != 2 {
		t.Errorf("expected count=2, got %v", resp["count"])
	}
	if len(logStore.insertedEntries) != 2 {
		t.Errorf("expected 2 inserted entries, got %d", len(logStore.insertedEntries))
	}
}

func TestHandleIngestLogs_MsgpackInvalidPayload(t *testing.T) {
	h := newTestHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewReader([]byte{0xff, 0xfe}))
	req.Header.Set("Content-Type", "application/msgpack")
	rec := httptest.NewRecorder()

	h.HandleIngestLogs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	errMsg, _ := resp["error"].(string)
	if !bytes.Contains([]byte(errMsg), []byte("invalid msgpack")) {
		t.Errorf("expected error to mention 'invalid msgpack', got: %s", errMsg)
	}
}
