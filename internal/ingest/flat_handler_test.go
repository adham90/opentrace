package ingest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adham90/opentrace/internal/logstore/engine"
	logsingest "github.com/adham90/opentrace/internal/logstore/ingest"
)

func newTestFlatHandler(t *testing.T) *FlatHandler {
	t.Helper()
	dir := t.TempDir()
	e, err := engine.NewStore(dir, nil, logsingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { e.Close() })
	return &FlatHandler{Engine: e}
}

func TestFlatIngest_SingleEntry(t *testing.T) {
	h := newTestFlatHandler(t)

	body := `{
		"ts": "2026-04-04T12:41:00.123Z",
		"level": "error",
		"service": "billing-api",
		"env": "production",
		"message": "Failed to charge customer",
		"trace_id": "trace-xyz"
	}`

	req := httptest.NewRequest("POST", "/api/v2/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleFlatIngest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["count"].(float64) != 1 {
		t.Errorf("count: want 1, got %v", resp["count"])
	}
}

func TestFlatIngest_BatchArray(t *testing.T) {
	h := newTestFlatHandler(t)

	body := `[
		{"ts": "2026-04-04T12:41:00Z", "level": "info", "service": "api", "message": "msg1"},
		{"ts": "2026-04-04T12:41:01Z", "level": "error", "service": "api", "message": "msg2"},
		{"ts": "2026-04-04T12:41:02Z", "level": "debug", "service": "worker", "message": "msg3"}
	]`

	req := httptest.NewRequest("POST", "/api/v2/logs", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.HandleFlatIngest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["count"].(float64) != 3 {
		t.Errorf("count: want 3, got %v", resp["count"])
	}

	ids := resp["ids"].([]any)
	if len(ids) != 3 {
		t.Errorf("ids: want 3, got %d", len(ids))
	}
}

func TestFlatIngest_RichRequest(t *testing.T) {
	h := newTestFlatHandler(t)

	body := `{
		"ts": "2026-04-04T12:41:00.456Z",
		"level": "info",
		"service": "billing-api",
		"message": "POST /api/orders 201 1243ms",
		"event_type": "http.request",
		"trace_id": "trace-xyz",
		"method": "POST",
		"path": "/api/orders",
		"status": 201,
		"duration_ms": 1243,
		"controller": "OrdersController",
		"action": "create",
		"db_ms": 312,
		"db_count": 8,
		"body": {
			"queries": [{"sql": "SELECT 1", "duration_ms": 1.2}],
			"timeline": [{"t": "db", "n": "query", "ms": 1.2}]
		}
	}`

	req := httptest.NewRequest("POST", "/api/v2/logs", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.HandleFlatIngest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFlatIngest_MissingRequired(t *testing.T) {
	h := newTestFlatHandler(t)

	body := `{"ts": "2026-04-04T12:00:00Z", "service": "api"}`
	req := httptest.NewRequest("POST", "/api/v2/logs", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.HandleFlatIngest(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rec.Code)
	}
}

func TestFlatIngest_NoTimestamp(t *testing.T) {
	h := newTestFlatHandler(t)

	// Missing ts should auto-assign server time
	body := `{"level": "info", "service": "api", "message": "no timestamp"}`
	req := httptest.NewRequest("POST", "/api/v2/logs", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.HandleFlatIngest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
