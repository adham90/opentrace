package ingest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adham90/opentrace/internal/logstore/adapter"
	"github.com/adham90/opentrace/internal/logstore/engine"
	logsingest "github.com/adham90/opentrace/internal/logstore/ingest"
)

// newTestFlatHandler builds a *Handler backed by a real engine (via the store
// adapter) so flat ingest exercises the same pipeline as production.
func newTestFlatHandler(t *testing.T) *Handler {
	t.Helper()
	dir := t.TempDir()
	e, err := engine.NewStore(dir, nil, logsingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { e.Close() })
	return &Handler{LogStore: adapter.New(e)}
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

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d: %s", rec.Code, rec.Body.String())
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

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["count"].(float64) != 3 {
		t.Errorf("count: want 3, got %v", resp["count"])
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
		"handler": "OrdersController#create",
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

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d: %s", rec.Code, rec.Body.String())
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

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestFlatIngest_FractionalDurations reproduces the Node SDK contract bug: the
// SDK sends fractional duration_ms/db_ms (performance.now()), which previously
// made json.Unmarshal into an int field fail and rejected the WHOLE batch.
func TestFlatIngest_FractionalDurations(t *testing.T) {
	h := newTestFlatHandler(t)

	// Mixed batch: a request event with fractional timings plus a plain log.
	body := `[
		{"ts":"2026-04-04T12:41:00Z","level":"info","service":"api","message":"GET /x 200 12.35ms","event_type":"http.request","status":200,"duration_ms":12.35,"db_ms":3.14,"db_count":2,"render_ms":0.5},
		{"ts":"2026-04-04T12:41:01Z","level":"info","service":"api","message":"plain log"}
	]`

	req := httptest.NewRequest("POST", "/api/v2/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleFlatIngest(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("fractional durations rejected: status %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["count"].(float64) != 2 {
		t.Errorf("count: want 2 (whole batch stored), got %v", resp["count"])
	}
}
