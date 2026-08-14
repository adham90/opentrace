package encoding

import (
	"encoding/binary"
	"fmt"
)

// --- Encoding 1: Delta + Varint ---
// For monotonic int64 columns (id, timestamp, received_at).
// Stores a base value + zigzag-varint-encoded deltas.
// Handles non-monotonic data via zigzag encoding (negative deltas are fine).

// DeltaEncode encodes a slice of int64 values as base + zigzag-varint deltas + zstd.
// Returns: [8 bytes base] [zstd(zigzag-varint deltas)]
func DeltaEncode(values []int64) []byte {
	if len(values) == 0 {
		return nil
	}

	base := values[0]

	// Encode deltas as zigzag varints
	deltaBuf := make([]byte, 0, len(values)*2)
	prev := base
	for _, v := range values {
		delta := v - prev
		deltaBuf = PutUvarint(deltaBuf, ZigZagEncode(delta))
		prev = v
	}

	compressed := Compress(deltaBuf)

	// [8 bytes base] [compressed deltas]
	result := make([]byte, 8, 8+len(compressed))
	binary.LittleEndian.PutUint64(result, uint64(base))
	result = append(result, compressed...)
	return result
}

// DeltaDecode decodes base + zigzag-varint deltas + zstd back to int64 values.
func DeltaDecode(data []byte, count int) ([]int64, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("delta data too short: %d bytes", len(data))
	}

	base := int64(binary.LittleEndian.Uint64(data[:8]))

	// Deltas are varints: at most MaxVarintBytes per value.
	raw, err := DecompressBounded(data[8:], DecodeCeiling(count, MaxVarintBytes))
	if err != nil {
		return nil, fmt.Errorf("decompress delta: %w", err)
	}

	result := make([]int64, 0, count)
	prev := base
	offset := 0
	for i := 0; i < count && offset < len(raw); i++ {
		encoded, n := binary.Uvarint(raw[offset:])
		if n <= 0 {
			return nil, fmt.Errorf("invalid delta varint at offset %d", offset)
		}
		delta := ZigZagDecode(encoded)
		prev += delta
		result = append(result, prev)
		offset += n
	}

	return result, nil
}
