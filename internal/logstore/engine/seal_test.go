package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/index"
	"github.com/adham90/opentrace/internal/logstore/wal"
)

func TestSealBasic(t *testing.T) {
	dir := t.TempDir()
	segDir := filepath.Join(dir, "2026-04-04T12")
	os.MkdirAll(segDir, 0o755)

	segHour := int64(493140) // 2026-04-04T12 in hours since epoch

	// Write a WAL file
	walPath := filepath.Join(segDir, "sealing_2026-04-04T12.wal")
	entries := testSealEntries(segHour)
	writeTestWAL(t, walPath, entries)

	// Seal
	meta, err := Seal(walPath, segHour)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if meta == nil {
		t.Fatal("meta should not be nil")
	}

	// Verify meta
	if meta.EntryCount != len(entries) {
		t.Errorf("entry count: want %d, got %d", len(entries), meta.EntryCount)
	}
	if len(meta.Chunks) != 1 {
		t.Errorf("chunk count: want 1, got %d", len(meta.Chunks))
	}

	// Verify counts
	if meta.Counts.ByLevel["info"] != 4 {
		t.Errorf("info count: want 4, got %d", meta.Counts.ByLevel["info"])
	}
	if meta.Counts.ByLevel["error"] != 1 {
		t.Errorf("error count: want 1, got %d", meta.Counts.ByLevel["error"])
	}
	if meta.Counts.ByLevel["debug"] != 1 {
		t.Errorf("debug count: want 1, got %d", meta.Counts.ByLevel["debug"])
	}

	// Verify files exist
	if _, err := os.Stat(filepath.Join(segDir, "chunk_000.col")); err != nil {
		t.Error("chunk_000.col should exist")
	}
	if _, err := os.Stat(filepath.Join(segDir, "chunk_000.idx")); err != nil {
		t.Error("chunk_000.idx should exist")
	}
	if _, err := os.Stat(filepath.Join(segDir, "meta.json")); err != nil {
		t.Error("meta.json should exist")
	}
	if !IsSealComplete(segDir) {
		t.Error(".seal_complete marker should exist")
	}

	// WAL should be deleted
	if _, err := os.Stat(walPath); !os.IsNotExist(err) {
		t.Error("WAL should be deleted after seal")
	}

	// Verify chunk is readable
	r, err := chunk.OpenReader(filepath.Join(segDir, "chunk_000.col"))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()

	if r.EntryCount != len(entries) {
		t.Errorf("chunk entry count: want %d, got %d", len(entries), r.EntryCount)
	}

	// Read and verify some columns
	levels, err := r.ReadDictBitpack("level")
	if err != nil {
		t.Fatalf("ReadDictBitpack(level): %v", err)
	}
	for i, e := range entries {
		if levels[i] != e.Level {
			t.Errorf("level[%d]: want %q, got %q", i, e.Level, levels[i])
		}
	}

	messages, err := r.ReadZstdBlockStrings("message")
	if err != nil {
		t.Fatalf("ReadZstdBlockStrings(message): %v", err)
	}
	for i, e := range entries {
		if messages[i] != e.Message {
			t.Errorf("message[%d]: want %q, got %q", i, e.Message, messages[i])
		}
	}

	// Verify inverted index is searchable
	idx, err := index.OpenReader(filepath.Join(segDir, "chunk_000.idx"))
	if err != nil {
		t.Fatalf("OpenReader(idx): %v", err)
	}

	ids, err := idx.Search("card declined")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(ids) != 1 || ids[0] != 1 {
		t.Errorf("search 'card declined': want [1], got %v", ids)
	}

	ids, err = idx.Search("cache")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(ids) != 1 || ids[0] != 2 {
		t.Errorf("search 'cache': want [2], got %v", ids)
	}

	t.Logf("seal: %d entries → chunk %d bytes + index",
		len(entries),
		fileSize(t, filepath.Join(segDir, "chunk_000.col")))
}

func TestSealMultiChunk(t *testing.T) {
	dir := t.TempDir()
	segDir := filepath.Join(dir, "2026-04-04T12")
	os.MkdirAll(segDir, 0o755)

	segHour := int64(493140)

	// Create more entries than chunkSize
	var entries []chunk.Entry
	baseTs := int64(1743760800000)
	for i := range chunkSize + 100 { // 50100 entries → 2 chunks
		entries = append(entries, chunk.Entry{
			ID:         IDForPosition(segHour, i),
			Ts:         baseTs + int64(i)*12,
			ReceivedAt: baseTs + int64(i)*12 + 1,
			Level:      "info",
			Service:    "api",
			Message:    "test entry",
		})
	}

	walPath := filepath.Join(segDir, "sealing.wal")
	writeTestWAL(t, walPath, entries)

	meta, err := Seal(walPath, segHour)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if len(meta.Chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(meta.Chunks))
	}

	if meta.Chunks[0].EntryCount != chunkSize {
		t.Errorf("chunk 0 count: want %d, got %d", chunkSize, meta.Chunks[0].EntryCount)
	}
	if meta.Chunks[1].EntryCount != 100 {
		t.Errorf("chunk 1 count: want 100, got %d", meta.Chunks[1].EntryCount)
	}

	// Both chunk files should exist
	if _, err := os.Stat(filepath.Join(segDir, "chunk_000.col")); err != nil {
		t.Error("chunk_000.col should exist")
	}
	if _, err := os.Stat(filepath.Join(segDir, "chunk_001.col")); err != nil {
		t.Error("chunk_001.col should exist")
	}
	if _, err := os.Stat(filepath.Join(segDir, "chunk_000.idx")); err != nil {
		t.Error("chunk_000.idx should exist")
	}
	if _, err := os.Stat(filepath.Join(segDir, "chunk_001.idx")); err != nil {
		t.Error("chunk_001.idx should exist")
	}

	t.Logf("multi-chunk: %d entries → %d chunks", len(entries), len(meta.Chunks))
}

func TestSealEmpty(t *testing.T) {
	dir := t.TempDir()
	segDir := filepath.Join(dir, "2026-04-04T12")
	os.MkdirAll(segDir, 0o755)

	// Empty WAL
	walPath := filepath.Join(segDir, "sealing.wal")
	os.WriteFile(walPath, nil, 0o644)

	meta, err := Seal(walPath, 493140)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if meta != nil {
		t.Error("empty WAL should return nil meta")
	}
}

func TestSealHistogram(t *testing.T) {
	dir := t.TempDir()
	segDir := filepath.Join(dir, "2026-04-04T12")
	os.MkdirAll(segDir, 0o755)

	segHour := int64(493140)

	entries := testSealEntries(segHour)
	walPath := filepath.Join(segDir, "sealing.wal")
	writeTestWAL(t, walPath, entries)

	meta, err := Seal(walPath, segHour)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if len(meta.Histogram) == 0 {
		t.Error("histogram should not be empty")
	}

	totalFromHistogram := 0
	errorsFromHistogram := 0
	for _, bucket := range meta.Histogram {
		totalFromHistogram += bucket.Total
		errorsFromHistogram += bucket.Errors
	}

	if totalFromHistogram != len(entries) {
		t.Errorf("histogram total: want %d, got %d", len(entries), totalFromHistogram)
	}
	if errorsFromHistogram != meta.Counts.ByLevel["error"]+meta.Counts.ByLevel["fatal"] {
		t.Errorf("histogram errors: want %d, got %d",
			meta.Counts.ByLevel["error"]+meta.Counts.ByLevel["fatal"], errorsFromHistogram)
	}

	t.Logf("histogram: %d minute buckets", len(meta.Histogram))
}

func TestSealMetaJSON(t *testing.T) {
	dir := t.TempDir()
	segDir := filepath.Join(dir, "2026-04-04T12")
	os.MkdirAll(segDir, 0o755)

	segHour := int64(493140)
	entries := testSealEntries(segHour)
	walPath := filepath.Join(segDir, "sealing.wal")
	writeTestWAL(t, walPath, entries)

	_, err := Seal(walPath, segHour)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Read and parse meta.json
	metaData, err := os.ReadFile(filepath.Join(segDir, "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}

	var meta SegmentMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf("parse meta.json: %v", err)
	}

	if meta.EntryCount != len(entries) {
		t.Errorf("meta entry_count: want %d, got %d", len(entries), meta.EntryCount)
	}

	// Dictionaries should be present
	if len(meta.Dicts["service"]) == 0 {
		t.Error("service dictionary should not be empty")
	}
	if len(meta.Dicts["level"]) == 0 {
		t.Error("level dictionary should not be empty")
	}

	t.Logf("meta.json:\n%s", metaData)
}

func TestCleanIncompleteSeal(t *testing.T) {
	dir := t.TempDir()
	segDir := filepath.Join(dir, "2026-04-04T12")
	os.MkdirAll(segDir, 0o755)

	// Create partial files
	os.WriteFile(filepath.Join(segDir, "chunk_000.col"), []byte("partial"), 0o644)
	os.WriteFile(filepath.Join(segDir, "chunk_000.idx"), []byte("partial"), 0o644)
	os.WriteFile(filepath.Join(segDir, "meta.json"), []byte("{}"), 0o644)

	CleanIncompleteSeal(segDir)

	if _, err := os.Stat(filepath.Join(segDir, "chunk_000.col")); !os.IsNotExist(err) {
		t.Error("chunk_000.col should be deleted")
	}
	if _, err := os.Stat(filepath.Join(segDir, "chunk_000.idx")); !os.IsNotExist(err) {
		t.Error("chunk_000.idx should be deleted")
	}
	if _, err := os.Stat(filepath.Join(segDir, "meta.json")); !os.IsNotExist(err) {
		t.Error("meta.json should be deleted")
	}
}

// --- helpers ---

func testSealEntries(segHour int64) []chunk.Entry {
	fa := false
	body := json.RawMessage(`{"queries":[{"sql":"SELECT 1"}]}`)
	baseTs := int64(1743760800000)

	return []chunk.Entry{
		{
			ID: IDForPosition(segHour, 0), Ts: baseTs, ReceivedAt: baseTs + 1,
			Level: "info", Service: "billing-api", Env: "production",
			Message: "POST /api/orders 201 1243ms",
		},
		{
			ID: IDForPosition(segHour, 1), Ts: baseTs + 100, ReceivedAt: baseTs + 101,
			Level: "error", Service: "billing-api", Env: "production",
			Message:          "Failed to charge customer: card declined",
			TraceID:          "trace-xyz",
			ErrorClass:       "PaymentError",
			ErrorFingerprint: "abc123",
		},
		{
			ID: IDForPosition(segHour, 2), Ts: baseTs + 200, ReceivedAt: baseTs + 201,
			Level: "info", Service: "auth",
			Message: "Cache hit for user 42",
		},
		{
			ID: IDForPosition(segHour, 3), Ts: baseTs + 300, ReceivedAt: baseTs + 301,
			Level: "info", Service: "billing-api", Env: "production",
			Message: "GET /api/users 200 45ms", EventType: "http.request",
			Method: "GET", Path: "/api/users", Status: 200, DurationMs: 45,
			NPlusOne: &fa,
		},
		{
			ID: IDForPosition(segHour, 4), Ts: baseTs + 400, ReceivedAt: baseTs + 401,
			Level: "debug", Service: "worker",
			Message: "Job completed: ProcessOrderJob",
		},
		{
			ID: IDForPosition(segHour, 5), Ts: baseTs + 500, ReceivedAt: baseTs + 501,
			Level: "info", Service: "billing-api",
			Message: "POST /api/payments 201 800ms",
			Body:    body,
		},
	}
}

func writeTestWAL(t *testing.T, path string, entries []chunk.Entry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create WAL: %v", err)
	}
	defer f.Close()

	for i := range entries {
		data := wal.MarshalEntry(&entries[i])
		if _, err := f.Write(data); err != nil {
			t.Fatalf("write WAL entry: %v", err)
		}
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Size()
}
