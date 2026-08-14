package encoding

import (
	"encoding/binary"
	"fmt"
)

// --- Encoding 6: Zstd Block ---
// For variable-length string/byte columns (message, body).
// Stores length-prefixed values, then zstd compresses the entire block.

// ZstdBlockEncodeStrings encodes a slice of strings as length-prefixed + zstd.
func ZstdBlockEncodeStrings(values []string) []byte {
	// Pre-allocate: average string ~100 bytes + 2 byte prefix
	buf := make([]byte, 0, len(values)*102)
	for _, v := range values {
		buf = AppendLenString(buf, v)
	}
	return Compress(buf)
}

// ZstdBlockDecodeStrings decodes zstd + length-prefixed strings using the given
// length framing (LenUint16 for chunk v1 files, LenExtended for v2+).
func ZstdBlockDecodeStrings(data []byte, count int, f LenFormat) ([]string, error) {
	raw, err := DecompressBounded(data, DecodeCeiling(count, MaxColumnValueBytes+MaxLenFramingBytes))
	if err != nil {
		return nil, fmt.Errorf("decompress zstd block strings: %w", err)
	}

	result := make([]string, 0, count)
	offset := 0
	for i := 0; i < count; i++ {
		var v string
		v, offset, err = f.ReadString(raw, offset)
		if err != nil {
			return nil, fmt.Errorf("zstd block string %d: %w", i, err)
		}
		result = append(result, v)
	}
	return result, nil
}

// ZstdBlockEncodeInt64 encodes a slice of int64 as raw LE bytes + zstd.
// Used for ts column (not delta-encoded, may not be monotonic).
func ZstdBlockEncodeInt64(values []int64) []byte {
	buf := make([]byte, len(values)*8)
	for i, v := range values {
		binary.LittleEndian.PutUint64(buf[i*8:], uint64(v))
	}
	return Compress(buf)
}

// ZstdBlockDecodeInt64 decodes zstd + raw LE int64 values.
func ZstdBlockDecodeInt64(data []byte, count int) ([]int64, error) {
	raw, err := DecompressBounded(data, DecodeCeiling(count, 8))
	if err != nil {
		return nil, fmt.Errorf("decompress zstd block int64: %w", err)
	}

	result := make([]int64, count)
	for i := range count {
		if i*8+8 > len(raw) {
			break
		}
		result[i] = int64(binary.LittleEndian.Uint64(raw[i*8:]))
	}
	return result, nil
}
