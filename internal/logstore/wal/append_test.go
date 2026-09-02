package wal

import (
	"bytes"
	"testing"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

// TestAppendEntryFramesEachRecord guards the batch serialization used by the
// WAL writer. AppendEntry patches a length prefix into the buffer after writing
// the payload; if it patches at offset 0 instead of at the record's own start,
// the first record's length is overwritten with the length of everything
// written so far and the whole WAL becomes unreadable from record two on.
func TestAppendEntryFramesEachRecord(t *testing.T) {
	entries := []chunk.Entry{
		{ID: 1, Ts: 1000, Level: "info", Service: "api", Message: "first"},
		{ID: 2, Ts: 2000, Level: "error", Service: "web", Message: "second, a longer message"},
		{ID: 3, Ts: 3000, Level: "warn", Service: "worker", Message: "third", Body: []byte(`{"a":1}`)},
	}

	var buf []byte
	for i := range entries {
		buf = AppendEntry(buf, &entries[i])
	}

	got, consumed, err := ReadEntriesFrom(bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("ReadEntriesFrom: %v", err)
	}
	if consumed != int64(len(buf)) {
		t.Errorf("consumed %d bytes of %d", consumed, len(buf))
	}
	if len(got) != len(entries) {
		t.Fatalf("read %d entries, wrote %d", len(got), len(entries))
	}
	for i := range entries {
		if got[i].ID != entries[i].ID || got[i].Message != entries[i].Message {
			t.Errorf("entry %d = {id:%d msg:%q}, want {id:%d msg:%q}",
				i, got[i].ID, got[i].Message, entries[i].ID, entries[i].Message)
		}
	}
}

// TestAppendEntryMatchesMarshalEntry keeps the batching path byte-identical to
// the single-entry path, so the on-disk format cannot drift between them.
func TestAppendEntryMatchesMarshalEntry(t *testing.T) {
	e := chunk.Entry{ID: 7, Ts: 42, Level: "info", Service: "api", Message: "hello", Body: []byte(`{"k":"v"}`)}
	if got, want := AppendEntry(nil, &e), MarshalEntry(&e); !bytes.Equal(got, want) {
		t.Errorf("AppendEntry produced %d bytes, MarshalEntry %d; they must be identical", len(got), len(want))
	}
}

// TestReadEntriesFromStopsAtTornTail checks that a partially written trailing
// record does not advance the consumed offset — the WAL cache re-reads from
// there, so a wrong offset would skip the record once it is completed.
func TestReadEntriesFromStopsAtTornTail(t *testing.T) {
	e1 := chunk.Entry{ID: 1, Ts: 1, Level: "info", Service: "api", Message: "complete"}
	e2 := chunk.Entry{ID: 2, Ts: 2, Level: "info", Service: "api", Message: "torn"}

	full := AppendEntry(nil, &e1)
	whole := len(full)
	full = AppendEntry(full, &e2)
	torn := full[:len(full)-3] // drop the tail of the second record

	got, consumed, err := ReadEntriesFrom(bytes.NewReader(torn))
	if err == nil {
		t.Fatal("want an error for a torn trailing record")
	}
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("want the intact prefix (1 entry, id 1), got %d entries", len(got))
	}
	if consumed != int64(whole) {
		t.Errorf("consumed = %d, want %d (the torn record must not be counted)", consumed, whole)
	}
}
