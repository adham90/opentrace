package chunk

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenReaderRejectsCorruptHeader guards the crash bug: a corrupt directory
// offset made the reader compute a negative/huge allocation and panic inside an
// unrecovered search goroutine, crashing the whole server. It must now return an
// error instead.
func TestOpenReaderRejectsCorruptHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.col")

	w, err := NewWriter(path, []Entry{{Ts: 1, Level: "info", Service: "s", Message: "m"}})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.WriteAllColumns(); err != nil {
		t.Fatalf("WriteAllColumns: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Corrupt the directory offset (header bytes 16..24) to point far past EOF.
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open for corruption: %v", err)
	}
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], 1<<40) // absurd offset
	if _, err := f.WriteAt(buf[:], 16); err != nil {
		t.Fatalf("corrupt write: %v", err)
	}
	f.Close()

	// Must return an error, not panic.
	r, err := OpenReader(path)
	if err == nil {
		r.Close()
		t.Fatal("expected an error opening a chunk with a corrupt directory offset, got nil")
	}
}
