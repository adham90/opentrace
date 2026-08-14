package ingest

import (
	"testing"
	"time"
)

// A job payload's timing must reach storage. Request rows carry duration on the
// RequestSummary; job and event rows had nowhere to put it, so duration_ms,
// db_ms, db_count and status were parsed and then silently dropped — discarding
// exactly what a job.perform payload exists to report.
func TestFlatToLogEntry_JobKeepsTiming(t *testing.T) {
	fe := flatEntry{
		Level:      "info",
		Message:    "job done",
		Service:    "svc",
		EventType:  "job.perform",
		JobClass:   "SendEmailJob",
		JobQueue:   "mailers",
		DurationMs: 850,
		DbMs:       40,
		DbCount:    7,
	}

	h := &Handler{}
	entry := h.flatToLogEntry(fe, time.Now().UTC())

	if entry.Kind != kindJob {
		t.Fatalf("Kind = %q, want %q", entry.Kind, kindJob)
	}
	if entry.RequestSummary != nil {
		t.Errorf("a job row must not be given a RequestSummary")
	}
	if entry.DurationMs != 850 {
		t.Errorf("DurationMs = %v, want 850 — job latency was dropped", entry.DurationMs)
	}
	if entry.DbMs != 40 {
		t.Errorf("DbMs = %v, want 40", entry.DbMs)
	}
	if entry.DbCount != 7 {
		t.Errorf("DbCount = %d, want 7", entry.DbCount)
	}
}

// An HTTP request handled inside a job context still carries method/path/status
// and must stay a request: classifying it as a job stripped its RequestSummary
// and removed it from every request view.
func TestDeriveKind_HTTPEvidenceOutranksJobFields(t *testing.T) {
	fe := flatEntry{
		Method:   "GET",
		Path:     "/api/orders",
		Status:   200,
		JobClass: "SomeJob",
	}
	if got := deriveKind(fe); got != kindRequest {
		t.Errorf("deriveKind = %q, want %q", got, kindRequest)
	}
}

// mem_delta_mb is stored as hundredths of a MB. Writing raw MB here read back
// 100x too small, while the RequestSummary path scaled correctly.
func TestFlatToLogEntry_MemDeltaIsScaled(t *testing.T) {
	h := &Handler{}
	entry := h.flatToLogEntry(flatEntry{Level: "info", Message: "m", MemDeltaMb: 3}, time.Now().UTC())
	if entry.MemDeltaMb != 300 {
		t.Errorf("MemDeltaMb = %d, want 300 (3 MB in hundredths)", entry.MemDeltaMb)
	}
}
