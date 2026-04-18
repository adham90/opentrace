package ingest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/engine"
	logsingest "github.com/adham90/opentrace/internal/logstore/ingest"
)

// TestFlatE2E_SDKToQueryRoundTrip tests the full round trip:
// SDK flat JSON → HTTP handler → engine.Ingest → seal → search
func TestFlatE2E_SDKToQueryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	e, err := engine.NewStore(dir, nil, logsingest.DefaultPIIConfig())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer e.Close()

	h := &FlatHandler{Engine: e}
	now := time.Now().UTC()

	// Simulate SDK sending a batch of mixed entries
	batch := `[
		{
			"ts": "` + now.Format(time.RFC3339Nano) + `",
			"level": "info",
			"service": "billing-api",
			"env": "production",
			"message": "Cache hit for user 42",
			"user_id": "42",
			"tenant_id": "7"
		},
		{
			"ts": "` + now.Format(time.RFC3339Nano) + `",
			"level": "error",
			"service": "billing-api",
			"env": "production",
			"message": "PaymentError: card declined",
			"trace_id": "trace-pay-001",
			"request_id": "req-001",
			"user_id": "42",
			"error_class": "PaymentError",
			"source_file": "app/services/billing.rb",
			"source_line": 99,
			"body": {
				"exception": {
					"backtrace": ["app/services/billing.rb:99"]
				},
				"request": {
					"params": {"email": "secret@example.com", "card": "4111111111111111"}
				}
			}
		},
		{
			"ts": "` + now.Format(time.RFC3339Nano) + `",
			"level": "info",
			"service": "billing-api",
			"env": "production",
			"version": "abc123",
			"message": "POST /api/orders 201 1243ms",
			"event_type": "http.request",
			"trace_id": "trace-req-001",
			"method": "POST",
			"path": "/api/orders",
			"status": 201,
			"duration_ms": 1243,
			"handler": "OrdersController#create",
			"db_ms": 312,
			"db_count": 8,
			"body": {
				"queries": [{"sql": "SELECT * FROM users", "duration_ms": 1.2}],
				"timeline": [{"t": "db", "n": "User Load", "ms": 1.2, "at": 0}],
				"logs": [{"level": "debug", "message": "Processing order", "at": 10}]
			}
		}
	]`

	// POST to handler
	req := httptest.NewRequest("POST", "/api/v2/logs", bytes.NewBufferString(batch))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleFlatIngest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	count := int(resp["count"].(float64))

	// 3 original + 1 expanded in-request log = 4
	if count != 4 {
		t.Fatalf("ingest count: want 4, got %d", count)
	}

	// Verify IDs returned
	ids := resp["ids"].([]any)
	if len(ids) != 4 {
		t.Fatalf("ids: want 4, got %d", len(ids))
	}

	// Seal
	if err := e.SealCurrentHour(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Search all
	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)
	res, err := e.Search(engine.SearchParams{Start: &start, End: &end, Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 4 {
		t.Errorf("search total: want 4, got %d", res.Total)
	}

	// Search by error level
	res, err = e.Search(engine.SearchParams{Level: "error", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("search error: want 1, got %d", res.Total)
	}
	if res.Total > 0 {
		errEntry := res.Entries[0]
		// Error fields extracted by pipeline
		if errEntry.ErrorClass != "PaymentError" {
			t.Errorf("error_class: %q", errEntry.ErrorClass)
		}
		if errEntry.ErrorFingerprint == "" {
			t.Error("fingerprint should be computed")
		}
		// PII scrubbed
		if bytes.Contains(errEntry.Body, []byte("secret@example.com")) {
			t.Error("email not scrubbed")
		}
		if bytes.Contains(errEntry.Body, []byte("4111111111111111")) {
			t.Error("credit card not scrubbed")
		}
	}

	// FTS
	res, err = e.Search(engine.SearchParams{Query: "cache hit", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("FTS: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("FTS 'cache hit': want 1, got %d", res.Total)
	}

	// GetByID
	firstID := int64(ids[0].(float64))
	entry, err := e.GetByID(firstID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if entry.Message != "Cache hit for user 42" {
		t.Errorf("GetByID message: %q", entry.Message)
	}

	// CountByLevel
	counts, err := e.CountByLevel(start, end, "", "")
	if err != nil {
		t.Fatalf("CountByLevel: %v", err)
	}
	total := 0
	for _, c := range counts {
		total += c
	}
	if total != 4 {
		t.Errorf("CountByLevel total: want 4, got %d", total)
	}

	// Histogram
	buckets, err := e.Histogram(start, end, time.Minute)
	if err != nil {
		t.Fatalf("Histogram: %v", err)
	}
	histTotal := 0
	for _, b := range buckets {
		histTotal += b.Total
	}
	if histTotal != 4 {
		t.Errorf("histogram total: want 4, got %d", histTotal)
	}

	t.Log("=== Flat E2E: SDK → HTTP → Ingest → Seal → Query PASSED ===")
}
