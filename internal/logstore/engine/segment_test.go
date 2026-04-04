package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSegmentManagerScan(t *testing.T) {
	dir := t.TempDir()

	// Create a sealed segment
	segHour := int64(493140) // 2026-04-04T12
	segDir := filepath.Join(dir, SegmentDirName(segHour))
	os.MkdirAll(segDir, 0o755)

	entries := testSealEntries(segHour)
	walPath := filepath.Join(segDir, "sealing.wal")
	writeTestWAL(t, walPath, entries)
	if _, err := Seal(walPath, segHour); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Create another segment
	segHour2 := segHour + 1
	segDir2 := filepath.Join(dir, SegmentDirName(segHour2))
	os.MkdirAll(segDir2, 0o755)

	entries2 := testSealEntries(segHour2)
	walPath2 := filepath.Join(segDir2, "sealing.wal")
	writeTestWAL(t, walPath2, entries2)
	if _, err := Seal(walPath2, segHour2); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Scan
	sm, err := NewSegmentManager(dir)
	if err != nil {
		t.Fatalf("NewSegmentManager: %v", err)
	}

	if sm.SegmentCount() != 2 {
		t.Errorf("segment count: want 2, got %d", sm.SegmentCount())
	}

	all := sm.AllSegments()
	if all[0].Hour != segHour {
		t.Errorf("first segment hour: want %d, got %d", segHour, all[0].Hour)
	}
	if all[1].Hour != segHour2 {
		t.Errorf("second segment hour: want %d, got %d", segHour2, all[1].Hour)
	}
}

func TestSegmentManagerRegister(t *testing.T) {
	dir := t.TempDir()
	sm, err := NewSegmentManager(dir)
	if err != nil {
		t.Fatalf("NewSegmentManager: %v", err)
	}

	meta := &SegmentMeta{
		Segment:    "2026-04-04T12",
		EntryCount: 100,
	}
	sm.Register(493140, meta)

	if sm.SegmentCount() != 1 {
		t.Errorf("segment count: want 1, got %d", sm.SegmentCount())
	}

	// Register another (earlier hour)
	meta2 := &SegmentMeta{Segment: "2026-04-04T11", EntryCount: 50}
	sm.Register(493139, meta2)

	if sm.SegmentCount() != 2 {
		t.Errorf("segment count: want 2, got %d", sm.SegmentCount())
	}

	// Should be sorted
	all := sm.AllSegments()
	if all[0].Hour != 493139 {
		t.Errorf("first should be T11, got hour %d", all[0].Hour)
	}
}

func TestSegmentManagerSegmentsInRange(t *testing.T) {
	dir := t.TempDir()
	sm, err := NewSegmentManager(dir)
	if err != nil {
		t.Fatalf("NewSegmentManager: %v", err)
	}

	// Register segments for T10, T11, T12, T13, T14
	for h := int64(493138); h <= 493142; h++ {
		sm.Register(h, &SegmentMeta{Segment: SegmentDirName(h), EntryCount: 10})
	}

	// Query T11:30 to T12:30 — should include T10, T11, T12, T13 (±1 hour buffer)
	start := TimeFromSegmentHour(493139).Add(30 * time.Minute)
	end := TimeFromSegmentHour(493140).Add(30 * time.Minute)

	segs := sm.SegmentsInRange(start, end)
	if len(segs) != 4 {
		hours := make([]int64, len(segs))
		for i, s := range segs {
			hours[i] = s.Hour
		}
		t.Errorf("segments in range: want 4, got %d (hours: %v)", len(segs), hours)
	}
}

func TestSegmentManagerPrune(t *testing.T) {
	dir := t.TempDir()

	// Create 3 sealed segments: now-48h, now-24h, now-1h
	now := time.Now().UTC()
	hours := []int64{
		SegmentHourFromTime(now.Add(-48 * time.Hour)),
		SegmentHourFromTime(now.Add(-24 * time.Hour)),
		SegmentHourFromTime(now.Add(-1 * time.Hour)),
	}

	for _, h := range hours {
		segDir := filepath.Join(dir, SegmentDirName(h))
		os.MkdirAll(segDir, 0o755)
		entries := testSealEntries(h)
		// Fix timestamps to match the hour
		baseTs := TimeFromSegmentHour(h).UnixMilli()
		for i := range entries {
			entries[i].ReceivedAt = baseTs + int64(i)*100
			entries[i].Ts = baseTs + int64(i)*100
			entries[i].ID = IDForPosition(h, i)
		}
		walPath := filepath.Join(segDir, "sealing.wal")
		writeTestWAL(t, walPath, entries)
		if _, err := Seal(walPath, h); err != nil {
			t.Fatalf("Seal hour %d: %v", h, err)
		}
	}

	sm, err := NewSegmentManager(dir)
	if err != nil {
		t.Fatalf("NewSegmentManager: %v", err)
	}

	if sm.SegmentCount() != 3 {
		t.Fatalf("before prune: want 3, got %d", sm.SegmentCount())
	}

	// Prune with 30-hour retention — should delete the 48h-old segment
	deleted, err := sm.Prune(30 * time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	if deleted != 1 {
		t.Errorf("deleted: want 1, got %d", deleted)
	}
	if sm.SegmentCount() != 2 {
		t.Errorf("after prune: want 2, got %d", sm.SegmentCount())
	}

	// The 48h-old directory should be gone
	oldDir := filepath.Join(dir, SegmentDirName(hours[0]))
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("old segment directory should be deleted")
	}
}

func TestSegmentManagerFindByID(t *testing.T) {
	dir := t.TempDir()
	sm, err := NewSegmentManager(dir)
	if err != nil {
		t.Fatalf("NewSegmentManager: %v", err)
	}

	hour := int64(493140)
	sm.Register(hour, &SegmentMeta{Segment: SegmentDirName(hour), EntryCount: 100})

	// Find by ID that belongs to this segment
	id := EncodeID(hour, 0, 42)
	seg := sm.FindSegmentByID(id)
	if seg == nil {
		t.Fatal("should find segment for ID")
	}
	if seg.Hour != hour {
		t.Errorf("found wrong segment: hour %d, want %d", seg.Hour, hour)
	}

	// Find by ID from non-existent segment
	id2 := EncodeID(999999, 0, 0)
	seg2 := sm.FindSegmentByID(id2)
	if seg2 != nil {
		t.Error("should not find segment for non-existent hour")
	}
}

func TestSegmentManagerEmpty(t *testing.T) {
	dir := t.TempDir()
	sm, err := NewSegmentManager(dir)
	if err != nil {
		t.Fatalf("NewSegmentManager: %v", err)
	}

	if sm.SegmentCount() != 0 {
		t.Errorf("empty: want 0, got %d", sm.SegmentCount())
	}

	segs := sm.SegmentsInRange(time.Now().Add(-time.Hour), time.Now())
	if len(segs) != 0 {
		t.Errorf("empty range: want 0, got %d", len(segs))
	}
}

func TestParseSegmentHour(t *testing.T) {
	hour := parseSegmentHour("2026-04-04T12")
	expected := SegmentHourFromTime(time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC))
	if hour != expected {
		t.Errorf("parseSegmentHour: want %d, got %d", expected, hour)
	}

	// Invalid format
	if parseSegmentHour("invalid") != 0 {
		t.Error("invalid should return 0")
	}
}
