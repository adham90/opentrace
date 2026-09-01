package engine

import (
	"fmt"

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
	r *chunkpkg.Reader
	// shared caches decoded columns across queries; nil disables it. path
	// identifies the chunk file within it.
	shared  *columnCache
	path    string
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
	return newCachedChunkColumns(r, nil, "")
}

// newCachedChunkColumns is newChunkColumns backed by a shared decoded-column
// cache. Sealed chunks never change, so a decoding stays valid for the life of
// the file; the per-scan maps below stay as the first-level lookup.
func newCachedChunkColumns(r *chunkpkg.Reader, shared *columnCache, path string) *chunkColumns {
	return &chunkColumns{
		r:       r,
		shared:  shared,
		path:    path,
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

// memoColumn returns the decoded column, decoding it at most once per scan and —
// when a shared cache is attached — at most once per chunk across queries.
//
// The returned slice is shared with other scans and must be treated as
// read-only. sizeOf is nil for columns that must not be shared (see
// columnCache's note on the body column).
func memoColumn[T any](c *chunkColumns, cache map[string][]T, name string, read func(string) ([]T, error), sizeOf func([]T) int64) ([]T, error) {
	if err, ok := c.errs[name]; ok {
		return nil, err
	}
	if v, ok := cache[name]; ok {
		return v, nil
	}
	key := columnKey{chunkPath: c.path, column: name}
	shareable := sizeOf != nil && c.shared != nil && c.path != ""
	if shareable {
		if v, ok := c.shared.get(key); ok {
			typed, ok := v.([]T)
			if ok {
				cache[name] = typed
				return typed, nil
			}
		}
		// Coalesce a cold decode across concurrent queries. Without this, every
		// request missing the same immutable column allocated its own full
		// decompression before racing to put an identical value in the LRU.
		loaded, err, _ := c.shared.loads.Do(key.chunkPath+"\x00"+key.column, func() (any, error) {
			if existing, ok := c.shared.get(key); ok {
				return existing, nil
			}
			value, readErr := read(name)
			if readErr != nil {
				return nil, readErr
			}
			c.shared.put(key, value, sizeOf(value))
			return value, nil
		})
		if err != nil {
			c.errs[name] = err
			return nil, err
		}
		typed, ok := loaded.([]T)
		if !ok {
			return nil, fmt.Errorf("cached column %q has unexpected type %T", name, loaded)
		}
		cache[name] = typed
		return typed, nil
	}
	v, err := read(name)
	if err != nil {
		c.errs[name] = err
		return nil, err
	}
	cache[name] = v
	if shareable {
		c.shared.put(key, v, sizeOf(v))
	}
	return v, nil
}

func (c *chunkColumns) deltaInt64(name string) ([]int64, error) {
	return memoColumn(c, c.ints, name, c.r.ReadDeltaInt64, sizeOfInt64s)
}

func (c *chunkColumns) zstdInt64(name string) ([]int64, error) {
	return memoColumn(c, c.ints, name, c.r.ReadZstdInt64, sizeOfInt64s)
}

func (c *chunkColumns) dictBitpack(name string) ([]string, error) {
	return memoColumn(c, c.strs, name, c.r.ReadDictBitpack, sizeOfStrings)
}

func (c *chunkColumns) dictZstd(name string) ([]string, error) {
	return memoColumn(c, c.strs, name, c.r.ReadDictZstd, sizeOfStrings)
}

func (c *chunkColumns) zstdBlockStrings(name string) ([]string, error) {
	return memoColumn(c, c.strs, name, c.r.ReadZstdBlockStrings, sizeOfStrings)
}

func (c *chunkColumns) sparseStrings(name string) ([]string, error) {
	return memoColumn(c, c.strs, name, c.r.ReadSparseStrings, sizeOfStrings)
}

func (c *chunkColumns) sparseInt64(name string) ([]*int64, error) {
	return memoColumn(c, c.optInts, name, c.r.ReadSparseInt64, sizeOfOptInt64s)
}

func (c *chunkColumns) sparseBool(name string) ([]*bool, error) {
	return memoColumn(c, c.bools, name, c.r.ReadSparseBool, sizeOfBools)
}

func (c *chunkColumns) sparseBytes(name string) ([][]byte, error) {
	// Shared like every other column; chunkRow.sparseBytes copies the row's
	// bytes on the way into an Entry so a caller cannot write through to the
	// cached column. Caching it matters because decoding a body column is
	// proportional to the whole chunk, which a single-entry lookup would
	// otherwise pay in full.
	return memoColumn(c, c.blobs, name, c.r.ReadSparseBytes, sizeOfBlobs)
}
