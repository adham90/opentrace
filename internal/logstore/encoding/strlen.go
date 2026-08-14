package encoding

import (
	"encoding/binary"
	"fmt"
)

// --- Length-prefixed value framing ---
//
// Every variable-length value in the WAL and in chunk columns is stored as a
// length prefix followed by the raw bytes. The original format used a bare
// uint16 prefix, which silently wrapped for values larger than 65535 bytes:
// the writer emitted len(v)%65536 as the prefix but appended all the bytes, so
// every subsequent value in the stream was misframed and the whole record or
// column decoded to garbage.
//
// LenExtended fixes that without giving up on old files: the prefix is still a
// uint16, but the value 0xFFFF is reserved as an escape meaning "the real
// length follows as a uint32". Values below 65535 bytes are byte-identical to
// the legacy framing; larger values round-trip exactly.
//
// Readers must know which framing a file uses, so the format is selected per
// container: chunk files carry a version in their header (v1 = LenUint16,
// v2+ = LenExtended) and WAL records carry FlagExtendedStrings. Encoders always
// write LenExtended.

// LenFormat selects the on-disk framing of a length-prefixed value.
type LenFormat uint8

const (
	// LenUint16 is the legacy framing: a bare uint16 length. Values longer than
	// maxUint16Len bytes cannot be represented and are rejected at encode time.
	LenUint16 LenFormat = 1
	// LenExtended is the current framing: a uint16 length where extendedLenMarker
	// escapes to a following uint32 length.
	LenExtended LenFormat = 2
)

const (
	// extendedLenMarker is the uint16 prefix value that escapes to a uint32 length.
	extendedLenMarker = 0xFFFF
	// maxUint16Len is the largest value representable by the legacy framing.
	maxUint16Len = extendedLenMarker - 1
	// MaxValueBytes bounds a single length-prefixed value. It exists so a corrupt
	// length can never be used for an unbounded allocation; decoders additionally
	// check the length against the bytes actually available.
	MaxValueBytes = 1 << 30 // 1 GiB
)

// AppendLenString appends s with a LenExtended length prefix.
func AppendLenString(buf []byte, s string) []byte {
	buf = appendLen(buf, len(s))
	return append(buf, s...)
}

// AppendLenBytes appends b with a LenExtended length prefix.
func AppendLenBytes(buf, b []byte) []byte {
	buf = appendLen(buf, len(b))
	return append(buf, b...)
}

func appendLen(buf []byte, n int) []byte {
	if n <= maxUint16Len {
		return binary.LittleEndian.AppendUint16(buf, uint16(n))
	}
	buf = binary.LittleEndian.AppendUint16(buf, extendedLenMarker)
	return binary.LittleEndian.AppendUint32(buf, uint32(n))
}

// ReadLen reads a length prefix in the given framing and returns the length and
// the offset of the first value byte.
func (f LenFormat) ReadLen(data []byte, off int) (int, int, error) {
	if off+2 > len(data) {
		return 0, off, fmt.Errorf("truncated length prefix at offset %d", off)
	}
	n := int(binary.LittleEndian.Uint16(data[off:]))
	off += 2
	if f == LenExtended && n == extendedLenMarker {
		if off+4 > len(data) {
			return 0, off, fmt.Errorf("truncated extended length prefix at offset %d", off)
		}
		n = int(binary.LittleEndian.Uint32(data[off:]))
		off += 4
	}
	if n < 0 || n > MaxValueBytes {
		return 0, off, fmt.Errorf("value length %d out of range at offset %d", n, off)
	}
	return n, off, nil
}

// ReadValue reads a length-prefixed value and returns it as a sub-slice of data
// (no copy) plus the offset just past it.
func (f LenFormat) ReadValue(data []byte, off int) ([]byte, int, error) {
	n, off, err := f.ReadLen(data, off)
	if err != nil {
		return nil, off, err
	}
	if off+n > len(data) {
		return nil, off, fmt.Errorf("truncated value at offset %d: need %d bytes, have %d", off, n, len(data)-off)
	}
	return data[off : off+n], off + n, nil
}

// ReadString reads a length-prefixed value and copies it into a string.
func (f LenFormat) ReadString(data []byte, off int) (string, int, error) {
	v, next, err := f.ReadValue(data, off)
	if err != nil {
		return "", next, err
	}
	return string(v), next, nil
}
