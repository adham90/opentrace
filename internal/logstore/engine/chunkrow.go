package engine

import (
	"errors"
	"fmt"

	chunkpkg "github.com/adham90/opentrace/internal/logstore/chunk"
)

// chunkRow reads one row out of a columnar chunk, accumulating the first error.
//
// Column reads used to be written as `if v, err := r.ReadX(col); err == nil`,
// which discarded every failure: a bad sector or a decode error turned into a
// blank message or a zero ID that the caller could not tell from real data.
// Only a genuinely absent column (a chunk written before that column existed)
// is answered with a zero value; everything else surfaces.
//
// Reads go through chunkColumns, which decodes each column once per chunk scan
// instead of once per row.
type chunkRow struct {
	c   *chunkColumns
	row int
	err error
}

// value pulls one row's value out of a column, treating a missing column as a
// zero value.
func chunkValue[T any](c *chunkRow, name string, read func(string) ([]T, error)) (T, bool) {
	var zero T
	if c.err != nil {
		return zero, false
	}
	vals, err := read(name)
	if err != nil {
		if errors.Is(err, chunkpkg.ErrColumnMissing) {
			return zero, false
		}
		c.err = fmt.Errorf("read column %q: %w", name, err)
		return zero, false
	}
	if c.row >= len(vals) {
		return zero, false
	}
	return vals[c.row], true
}

func (c *chunkRow) str(name string, read func(string) ([]string, error), dst *string) {
	if v, ok := chunkValue(c, name, read); ok {
		*dst = v
	}
}

func (c *chunkRow) i64(name string, read func(string) ([]int64, error), dst *int64) {
	if v, ok := chunkValue(c, name, read); ok {
		*dst = v
	}
}

// sparseInt reads a nullable int64 column into an int field.
func (c *chunkRow) sparseInt(name string, dst *int) {
	if v, ok := chunkValue(c, name, c.c.sparseInt64); ok && v != nil {
		*dst = int(*v)
	}
}

// sparseBool copies the value rather than storing the column's pointer: the
// decoded column is shared across queries through the column cache, so handing
// its pointer out in an Entry would let a caller mutating the field rewrite the
// chunk's value for everyone else.
func (c *chunkRow) sparseBool(name string, dst **bool) {
	v, ok := chunkValue(c, name, c.c.sparseBool)
	if !ok || v == nil {
		return
	}
	b := *v
	*dst = &b
}

// sparseBytes copies the row's blob out of the decoded column. The column is
// shared across queries through the column cache, and this value is handed to
// callers as chunk.Entry.Body — aliasing it would let a caller's write corrupt
// the chunk for every later reader. The copy is per returned row, not per
// scanned row, so it costs the page and not the scan.
func (c *chunkRow) sparseBytes(name string, dst *[]byte) {
	v, ok := chunkValue(c, name, c.c.sparseBytes)
	if !ok || v == nil {
		return
	}
	*dst = append([]byte(nil), v...)
}
