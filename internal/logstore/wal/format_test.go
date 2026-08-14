package wal

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

// roundTrip marshals an entry, feeds it through the record reader (the path
// seal and replay use) and returns the decoded entry.
func roundTrip(t *testing.T, e *chunk.Entry) chunk.Entry {
	t.Helper()
	entries, err := ReadEntries(bytes.NewReader(MarshalEntry(e)))
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	return entries[0]
}

// TestLargeFieldRoundTrip is the regression test for the critical WAL bug: a
// field over 65535 bytes used to get a length prefix of len%65536 while all its
// bytes were appended, desynchronizing the record and permanently wedging the
// hour (seal failed forever, and replay could block server startup).
func TestLargeFieldRoundTrip(t *testing.T) {
	for _, size := range []int{65534, 65535, 65536, 70000, 500000} {
		t.Run(fmt.Sprintf("bytes=%d", size), func(t *testing.T) {
			big := strings.Repeat("S", size)
			e := &chunk.Entry{
				ID: 1, Ts: 2, ReceivedAt: 3,
				Level: "error", Service: "billing", Message: big,
				Env: "production", EventType: "http.request", TraceID: "t-1",
				Path: big, ErrorMessage: big, Status: 500,
			}

			got := roundTrip(t, e)
			if got.Message != big {
				t.Fatalf("message: want %d bytes, got %d", size, len(got.Message))
			}
			if got.Path != big {
				t.Fatalf("path: want %d bytes, got %d", size, len(got.Path))
			}
			if got.ErrorMessage != big {
				t.Fatalf("error_message: want %d bytes, got %d", size, len(got.ErrorMessage))
			}
			// Fields after the big ones must still be intact — misframing used
			// to shred everything downstream.
			if got.Status != 500 || got.TraceID != "t-1" || got.Env != "production" {
				t.Fatalf("trailing fields corrupted: %+v", got)
			}
		})
	}
}

// TestManyLargeEntriesInOneWAL proves one oversized entry no longer poisons the
// records that follow it in the same file.
func TestManyLargeEntriesInOneWAL(t *testing.T) {
	big := strings.Repeat("B", 200000)
	want := []chunk.Entry{
		{ID: 1, Ts: 1, Level: "info", Service: "a", Message: "small"},
		{ID: 2, Ts: 2, Level: "error", Service: "a", Message: big},
		{ID: 3, Ts: 3, Level: "info", Service: "a", Message: "after"},
	}

	var buf bytes.Buffer
	for i := range want {
		buf.Write(MarshalEntry(&want[i]))
	}

	got, err := ReadEntries(&buf)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("want %d entries, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Message != want[i].Message {
			t.Fatalf("entry %d: id %d msg %d bytes; want id %d msg %d bytes",
				i, got[i].ID, len(got[i].Message), want[i].ID, len(want[i].Message))
		}
	}
}

// TestFieldTruncatedAtMax documents the explicit ceiling: beyond MaxFieldBytes
// a value is truncated (and logged), never misframed.
func TestFieldTruncatedAtMax(t *testing.T) {
	huge := strings.Repeat("H", MaxFieldBytes+5000)
	e := &chunk.Entry{ID: 1, Ts: 2, Level: "info", Service: "a", Message: huge}

	got := roundTrip(t, e)
	if len(got.Message) > MaxFieldBytes {
		t.Fatalf("message not truncated: %d bytes", len(got.Message))
	}
	if len(got.Message) != MaxFieldBytes {
		t.Fatalf("want exactly %d bytes (ASCII input), got %d", MaxFieldBytes, len(got.Message))
	}
	if !strings.HasPrefix(huge, got.Message) {
		t.Fatal("truncated message is not a prefix of the original")
	}
}

func TestTruncationKeepsUTF8Valid(t *testing.T) {
	// One ASCII byte then 2-byte runes: the MaxFieldBytes cut lands mid-rune
	// unless the truncation walks back to a rune boundary.
	huge := "a" + strings.Repeat("é", MaxFieldBytes)
	e := &chunk.Entry{ID: 1, Ts: 2, Level: "info", Service: "a", Message: huge}

	got := roundTrip(t, e)
	for _, r := range got.Message {
		if r == '�' {
			t.Fatal("truncation split a UTF-8 rune")
		}
	}
}

// TestUnknownLevelRoundTrip covers the round-trip asymmetry: an unknown level
// was rewritten to "info" in the WAL, so live (ring buffer) results and
// WAL/sealed results reported different levels for the same entry.
func TestUnknownLevelRoundTrip(t *testing.T) {
	for _, level := range []string{"critical", "notice", "CRITICAL", "verbose", "warning", "trace", "fatal"} {
		t.Run(level, func(t *testing.T) {
			e := &chunk.Entry{ID: 1, Ts: 2, Level: level, Service: "a", Message: "m"}
			got := roundTrip(t, e)

			want := level
			if canonical, known := levelToEnum[level]; known {
				want = enumToLevel[canonical] // "warning" canonicalizes to "warn"
			}
			if got.Level != want {
				t.Fatalf("level: want %q, got %q", want, got.Level)
			}
		})
	}
}

// TestLegacyRecordStillReadable builds a record in the pre-fix format (no
// FlagExtendedStrings, bare uint16 string lengths) and checks it still replays,
// so an existing active.wal survives the upgrade.
func TestLegacyRecordStillReadable(t *testing.T) {
	appendLegacyString := func(buf []byte, s string) []byte {
		buf = binary.LittleEndian.AppendUint16(buf, uint16(len(s)))
		return append(buf, s...)
	}

	var rec []byte
	rec = binary.LittleEndian.AppendUint64(rec, 42)   // id
	rec = binary.LittleEndian.AppendUint64(rec, 1000) // ts
	rec = binary.LittleEndian.AppendUint64(rec, 1001) // received_at
	rec = append(rec, levelToEnum["warn"])            // level enum
	rec = binary.LittleEndian.AppendUint64(rec, FlagEnv|FlagTraceID)
	rec = appendLegacyString(rec, "billing")       // service
	rec = appendLegacyString(rec, "legacy record") // message
	rec = appendLegacyString(rec, "production")    // env
	rec = appendLegacyString(rec, "trace-1")       // trace_id

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(len(rec)))
	buf.Write(rec)

	entries, err := ReadEntries(&buf)
	if err != nil {
		t.Fatalf("ReadEntries(legacy): %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	got := entries[0]
	if got.ID != 42 || got.Level != "warn" || got.Service != "billing" ||
		got.Message != "legacy record" || got.Env != "production" || got.TraceID != "trace-1" {
		t.Fatalf("legacy record decoded wrong: %+v", got)
	}
}

// TestTrailingBytesRejected makes sure a misframed record errors instead of
// parsing into silently garbled fields.
func TestTrailingBytesRejected(t *testing.T) {
	rec := MarshalEntry(&chunk.Entry{ID: 1, Ts: 2, Level: "info", Service: "a", Message: "m"})
	payload := append(append([]byte(nil), rec[4:]...), 0xDE, 0xAD)

	if _, err := UnmarshalEntry(payload); err == nil {
		t.Fatal("expected an error for a record with trailing bytes, got none")
	}
}

func TestReadEntriesRejectsAbsurdRecordLength(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(maxRecordBytes+1))
	buf.Write([]byte("junk"))

	if _, err := ReadEntries(&buf); err == nil {
		t.Fatal("expected an error for an out-of-range record length, got none")
	}
}
