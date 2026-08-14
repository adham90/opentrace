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

func (c *chunkRow) sparseBool(name string, dst **bool) {
	if v, ok := chunkValue(c, name, c.c.sparseBool); ok {
		*dst = v
	}
}

func (c *chunkRow) sparseBytes(name string, dst *[]byte) {
	if v, ok := chunkValue(c, name, c.c.sparseBytes); ok && v != nil {
		*dst = v
	}
}
