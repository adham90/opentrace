package engine

import (
	chunkpkg "github.com/adham90/opentrace/internal/logstore/chunk"
)

// chunkColumns memoizes decoded columns for the duration of a single chunk
// scan.
//
// chunk.Reader caches nothing: every Read* call re-reads the column bytes and
// runs the whole zstd/dictionary/bitmap decode again. readEntryFromChunk reads
// ~45 columns for one row, so materializing N rows of a chunk decoded every
// column N times — O(rows² × columns), which put a 20k-row hour at ~90s per
// query. Decoding each column once per scan and indexing rows into the decoded
// arrays makes that O(rows + columns).
//
// Scope and safety:
//   - One instance belongs to one chunk scan (searchChunk / GetByID). It is not
//     safe for concurrent use, and it must never be shared or cached across
//     queries: the decoded arrays are dropped with the instance when the scan
//     ends, so peak memory stays at "one chunk per in-flight scan" rather than
//     an unbounded process-wide cache.
//   - Columns are decoded lazily, so a query only pays for the columns it
//     actually touches.
type chunkColumns struct {
	r       *chunkpkg.Reader
	ints    map[string][]int64
	strs    map[string][]string
	optInts map[string][]*int64
	bools   map[string][]*bool
	blobs   map[string][][]byte
	// errs remembers failures so a broken column is not re-decoded (and
	// re-logged) once per row.
	errs map[string]error
}

func newChunkColumns(r *chunkpkg.Reader) *chunkColumns {
	return &chunkColumns{
		r:       r,
		ints:    make(map[string][]int64),
		strs:    make(map[string][]string),
		optInts: make(map[string][]*int64),
		bools:   make(map[string][]*bool),
		blobs:   make(map[string][][]byte),
		errs:    make(map[string]error),
	}
}

// entryCount reports how many rows the underlying chunk holds.
func (c *chunkColumns) entryCount() int { return c.r.EntryCount }

// hasColumn reports whether the chunk stores the named column.
func (c *chunkColumns) hasColumn(name string) bool { return c.r.HasColumn(name) }

// memoColumn returns the decoded column, decoding it at most once per scan.
func memoColumn[T any](c *chunkColumns, cache map[string][]T, name string, read func(string) ([]T, error)) ([]T, error) {
	if err, ok := c.errs[name]; ok {
		return nil, err
	}
	if v, ok := cache[name]; ok {
		return v, nil
	}
	v, err := read(name)
	if err != nil {
		c.errs[name] = err
		return nil, err
	}
	cache[name] = v
	return v, nil
}

func (c *chunkColumns) deltaInt64(name string) ([]int64, error) {
	return memoColumn(c, c.ints, name, c.r.ReadDeltaInt64)
}

func (c *chunkColumns) zstdInt64(name string) ([]int64, error) {
	return memoColumn(c, c.ints, name, c.r.ReadZstdInt64)
}

func (c *chunkColumns) dictBitpack(name string) ([]string, error) {
	return memoColumn(c, c.strs, name, c.r.ReadDictBitpack)
}

func (c *chunkColumns) dictZstd(name string) ([]string, error) {
	return memoColumn(c, c.strs, name, c.r.ReadDictZstd)
}

func (c *chunkColumns) zstdBlockStrings(name string) ([]string, error) {
	return memoColumn(c, c.strs, name, c.r.ReadZstdBlockStrings)
}

func (c *chunkColumns) sparseStrings(name string) ([]string, error) {
	return memoColumn(c, c.strs, name, c.r.ReadSparseStrings)
}

func (c *chunkColumns) sparseInt64(name string) ([]*int64, error) {
	return memoColumn(c, c.optInts, name, c.r.ReadSparseInt64)
}

func (c *chunkColumns) sparseBool(name string) ([]*bool, error) {
	return memoColumn(c, c.bools, name, c.r.ReadSparseBool)
}

func (c *chunkColumns) sparseBytes(name string) ([][]byte, error) {
	return memoColumn(c, c.blobs, name, c.r.ReadSparseBytes)
}
