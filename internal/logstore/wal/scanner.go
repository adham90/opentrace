package wal

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

// Scanner reads WAL records one at a time, reusing a single record buffer.
//
// It exists so a caller does not have to hold a whole file's worth of entries
// to process it. Sealing an hour used to read every record into one slice
// before writing the first chunk, which made peak memory a function of how busy
// the hour was rather than of the work being done.
//
// Nothing a Scanner produces aliases its internal buffer — UnmarshalEntryInto
// copies every string and decompresses the body into a fresh slice — so the
// buffer is safe to reuse across records.
type Scanner struct {
	r        io.Reader
	lenBuf   [4]byte
	rec      []byte
	consumed int64
	err      error
}

// NewScanner reads records from r. Wrap an *os.File in a bufio.Reader first;
// Scanner issues two reads per record and does no buffering of its own.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{r: r}
}

// Next decodes the next record into dst and reports whether one was read.
// It returns false at end of file and on the first malformed record; check Err
// to tell those apart.
func (s *Scanner) Next(dst *chunk.Entry) bool {
	if s.err != nil {
		return false
	}
	if _, err := io.ReadFull(s.r, s.lenBuf[:]); err != nil {
		if err != io.EOF {
			s.err = fmt.Errorf("read entry length: %w", err)
		}
		return false
	}

	entryLen := int(binary.LittleEndian.Uint32(s.lenBuf[:]))
	if entryLen < minRecordBytes || entryLen > maxRecordBytes {
		s.err = fmt.Errorf("invalid entry length: %d", entryLen)
		return false
	}
	if cap(s.rec) < entryLen {
		s.rec = make([]byte, entryLen)
	}
	s.rec = s.rec[:entryLen]

	if _, err := io.ReadFull(s.r, s.rec); err != nil {
		s.err = fmt.Errorf("read entry data: %w", err)
		return false
	}
	if err := UnmarshalEntryInto(s.rec, dst); err != nil {
		s.err = fmt.Errorf("unmarshal entry: %w", err)
		return false
	}
	s.consumed += int64(len(s.lenBuf) + entryLen)
	return true
}

// NextBatch fills up to max entries onto dst and returns the extended slice.
// It is the batching a chunked writer wants: read a chunk's worth, write it,
// reuse the slice for the next chunk.
func (s *Scanner) NextBatch(dst []chunk.Entry, max int) []chunk.Entry {
	for len(dst) < max {
		dst = dst[:len(dst)+1]
		if !s.Next(&dst[len(dst)-1]) {
			return dst[:len(dst)-1]
		}
	}
	return dst
}

// Consumed reports the byte offset after the last fully decoded record. A torn
// trailing record does not advance it, so a caller re-reading an append-only
// file picks the record up once it is complete.
func (s *Scanner) Consumed() int64 { return s.consumed }

// Err reports why scanning stopped, or nil at a clean end of file.
func (s *Scanner) Err() error { return s.err }
