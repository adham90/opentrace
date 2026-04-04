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
		buf = binary.LittleEndian.AppendUint16(buf, uint16(len(v)))
		buf = append(buf, v...)
	}
	return Compress(buf)
}

// ZstdBlockDecodeStrings decodes zstd + length-prefixed strings.
func ZstdBlockDecodeStrings(data []byte, count int) ([]string, error) {
	raw, err := Decompress(data)
	if err != nil {
		return nil, fmt.Errorf("decompress zstd block strings: %w", err)
	}

	result := make([]string, 0, count)
	offset := 0
	for i := 0; i < count; i++ {
		if offset+2 > len(raw) {
			return nil, fmt.Errorf("zstd block string %d: truncated length at offset %d", i, offset)
		}
		sLen := int(binary.LittleEndian.Uint16(raw[offset:]))
		offset += 2
		if offset+sLen > len(raw) {
			return nil, fmt.Errorf("zstd block string %d: truncated data (need %d, have %d)", i, sLen, len(raw)-offset)
		}
		result = append(result, string(raw[offset:offset+sLen]))
		offset += sLen
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
	raw, err := Decompress(data)
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
