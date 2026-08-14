package chunk

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeChunk(t *testing.T, entries []Entry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chunk.col")
	w, err := NewWriter(path, entries)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.WriteAllColumns(); err != nil {
		t.Fatalf("WriteAllColumns: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return path
}

// TestChunkRoundTripLargeValues covers the latent misframing bug in the chunk
// string encoders: a value over 65535 bytes used to get a wrapped length prefix
// and shift every later value in the column.
func TestChunkRoundTripLargeValues(t *testing.T) {
	for _, size := range []int{65534, 65535, 65536, 70000, 300000} {
		t.Run(fmt.Sprintf("bytes=%d", size), func(t *testing.T) {
			big := strings.Repeat("x", size)
			entries := []Entry{
				{ID: 1, Ts: 10, Level: "info", Service: "api", Message: "first", Path: "/a"},
				{ID: 2, Ts: 20, Level: "error", Service: "api", Message: big, Path: big},
				{ID: 3, Ts: 30, Level: "info", Service: "api", Message: "last", Path: "/c"},
			}

			r, err := OpenReader(writeChunk(t, entries))
			if err != nil {
				t.Fatalf("OpenReader: %v", err)
			}
			defer r.Close()

			msgs, err := r.ReadZstdBlockStrings("message")
			if err != nil {
				t.Fatalf("ReadZstdBlockStrings: %v", err)
			}
			paths, err := r.ReadSparseStrings("path")
			if err != nil {
				t.Fatalf("ReadSparseStrings: %v", err)
			}
			for i, e := range entries {
				if msgs[i] != e.Message {
					t.Fatalf("message %d: want %d bytes, got %d", i, len(e.Message), len(msgs[i]))
				}
				if paths[i] != e.Path {
					t.Fatalf("path %d: want %d bytes, got %d", i, len(e.Path), len(paths[i]))
				}
			}
		})
	}
}

// TestReadsLegacyVersion1Chunk proves the version bump did not orphan existing
// files: for values under 64KB the v1 and v2 framings are byte-identical, so a
// file stamped v1 must still open and decode.
func TestReadsLegacyVersion1Chunk(t *testing.T) {
	entries := []Entry{
		{ID: 1, Ts: 10, Level: "info", Service: "api", Message: "hello", Path: "/a"},
		{ID: 2, Ts: 20, Level: "warn", Service: "web", Message: "world", Path: "/b"},
	}
	path := writeChunk(t, entries)

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open chunk: %v", err)
	}
	var v [2]byte
	binary.LittleEndian.PutUint16(v[:], 1)
	if _, err := f.WriteAt(v[:], 4); err != nil {
		t.Fatalf("stamp version 1: %v", err)
	}
	f.Close()

	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader(v1): %v", err)
	}
	defer r.Close()
	if r.Version != 1 {
		t.Fatalf("want version 1, got %d", r.Version)
	}

	msgs, err := r.ReadZstdBlockStrings("message")
	if err != nil {
		t.Fatalf("ReadZstdBlockStrings: %v", err)
	}
	svcs, err := r.ReadDictZstd("service")
	if err != nil {
		t.Fatalf("ReadDictZstd: %v", err)
	}
	lvls, err := r.ReadDictBitpack("level")
	if err != nil {
		t.Fatalf("ReadDictBitpack: %v", err)
	}
	for i, e := range entries {
		if msgs[i] != e.Message || svcs[i] != e.Service || lvls[i] != e.Level {
			t.Fatalf("entry %d: got (%q,%q,%q), want (%q,%q,%q)",
				i, msgs[i], svcs[i], lvls[i], e.Message, e.Service, e.Level)
		}
	}
}

// TestChunkManyDistinctLevels guards the bitpacked level column: its dictionary
// indices are uint8, so an entry stream with more distinct levels than a byte
// can address must degrade, not panic.
func TestChunkManyDistinctLevels(t *testing.T) {
	entries := make([]Entry, 600)
	for i := range entries {
		entries[i] = Entry{ID: int64(i), Ts: int64(i), Level: fmt.Sprintf("lvl-%d", i), Service: "api", Message: "m"}
	}

	r, err := OpenReader(writeChunk(t, entries))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()

	levels, err := r.ReadDictBitpack("level")
	if err != nil {
		t.Fatalf("ReadDictBitpack: %v", err)
	}
	if len(levels) != len(entries) {
		t.Fatalf("want %d levels, got %d", len(entries), len(levels))
	}
	// Everything the dictionary could hold must be exact.
	for i, got := range levels {
		if got != "" && got != entries[i].Level {
			t.Fatalf("level %d: want %q, got %q", i, entries[i].Level, got)
		}
	}
}

// TestReadColumnPropagatesCorruption covers the swallowed-error bug: every
// Read* helper turned any readColumnRaw failure — including the corruption
// guard and real I/O errors — into valid-looking zero/empty data.
func TestReadColumnPropagatesCorruption(t *testing.T) {
	entries := []Entry{
		{ID: 1, Ts: 10, Level: "info", Service: "api", Message: "hello", Path: "/a"},
		{ID: 2, Ts: 20, Level: "info", Service: "api", Message: "world", Path: "/b"},
	}
	path := writeChunk(t, entries)

	// Point the "service" directory entry at an absurd compressed size so
	// readColumnRaw's corruption guard fires.
	corruptColumnSize(t, path, "service")

	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()

	got, err := r.ReadDictZstd("service")
	if err == nil {
		t.Fatalf("expected a corruption error, got %d fabricated values %q", len(got), got)
	}

	// A column that is genuinely absent (schema evolution) still yields defaults.
	missing, err := r.ReadSparseStrings("not_a_real_column")
	if err != nil {
		t.Fatalf("missing column should not be an error: %v", err)
	}
	if len(missing) != r.EntryCount {
		t.Fatalf("missing column: want %d defaults, got %d", r.EntryCount, len(missing))
	}
}

// TestReadColumnPropagatesIOError closes the underlying file so ReadAt fails.
func TestReadColumnPropagatesIOError(t *testing.T) {
	path := writeChunk(t, []Entry{{ID: 1, Ts: 1, Level: "info", Service: "api", Message: "m"}})

	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	if err := r.f.Close(); err != nil {
		t.Fatalf("close underlying file: %v", err)
	}

	if got, err := r.ReadZstdBlockStrings("message"); err == nil {
		t.Fatalf("expected an I/O error, got %q", got)
	}
	if got, err := r.ReadDeltaInt64("id"); err == nil {
		t.Fatalf("expected an I/O error, got %v", got)
	}
}

// corruptColumnSize rewrites one directory entry's compressed size to a value
// larger than the file, simulating a torn write.
func corruptColumnSize(t *testing.T, path, column string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read chunk: %v", err)
	}
	dirOffset := int(binary.LittleEndian.Uint64(raw[16:]))

	offset := dirOffset + 2 // skip the directory entry count
	for offset < len(raw) {
		nameLen := int(raw[offset])
		offset++
		name := string(raw[offset : offset+nameLen])
		offset += nameLen
		// type(1) + offset(8) then compressed size(4)
		sizeAt := offset + 9
		if name == column {
			binary.LittleEndian.PutUint32(raw[sizeAt:], 1<<30)
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatalf("write corrupted chunk: %v", err)
			}
			return
		}
		offset += 17
	}
	t.Fatalf("column %q not found in directory", column)
}
