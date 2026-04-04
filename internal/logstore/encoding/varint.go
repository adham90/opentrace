package encoding

import (
	"encoding/binary"
	"fmt"
	"math"
)

// PutVarint appends a varint-encoded int64 to dst and returns the extended slice.
func PutVarint(dst []byte, v int64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutVarint(buf[:], v)
	return append(dst, buf[:n]...)
}

// PutUvarint appends a uvarint-encoded uint64 to dst and returns the extended slice.
func PutUvarint(dst []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(dst, buf[:n]...)
}

// EncodeVarintSlice encodes a slice of int64 values as varints + zstd.
func EncodeVarintSlice(values []int64) []byte {
	// Pre-allocate: most varints are 1-3 bytes
	buf := make([]byte, 0, len(values)*3)
	for _, v := range values {
		buf = PutVarint(buf, v)
	}
	return Compress(buf)
}

// DecodeVarintSlice decodes zstd + varint encoded int64 values.
func DecodeVarintSlice(data []byte, count int) ([]int64, error) {
	raw, err := Decompress(data)
	if err != nil {
		return nil, fmt.Errorf("decompress varint slice: %w", err)
	}
	result := make([]int64, 0, count)
	offset := 0
	for offset < len(raw) {
		v, n := binary.Varint(raw[offset:])
		if n <= 0 {
			return nil, fmt.Errorf("invalid varint at offset %d", offset)
		}
		result = append(result, v)
		offset += n
	}
	return result, nil
}

// EncodeUvarintSlice encodes a slice of uint64 values as uvarints + zstd.
func EncodeUvarintSlice(values []uint64) []byte {
	buf := make([]byte, 0, len(values)*3)
	for _, v := range values {
		buf = PutUvarint(buf, v)
	}
	return Compress(buf)
}

// DecodeUvarintSlice decodes zstd + uvarint encoded uint64 values.
func DecodeUvarintSlice(data []byte, count int) ([]uint64, error) {
	raw, err := Decompress(data)
	if err != nil {
		return nil, fmt.Errorf("decompress uvarint slice: %w", err)
	}
	result := make([]uint64, 0, count)
	offset := 0
	for offset < len(raw) {
		v, n := binary.Uvarint(raw[offset:])
		if n <= 0 {
			return nil, fmt.Errorf("invalid uvarint at offset %d", offset)
		}
		result = append(result, v)
		offset += n
	}
	return result, nil
}

// ZigZagEncode converts a signed int64 to an unsigned uint64 using zigzag encoding.
// This makes small negative numbers small unsigned numbers (good for varint).
func ZigZagEncode(v int64) uint64 {
	return uint64((v << 1) ^ (v >> 63))
}

// ZigZagDecode converts a zigzag-encoded uint64 back to int64.
func ZigZagDecode(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

// --- Int-to-float helpers for sparse numeric columns ---

// Float64ToInt64 converts a float64 to int64 bits for storage.
func Float64ToInt64(f float64) int64 {
	return int64(math.Float64bits(f))
}

// Int64ToFloat64 converts int64 bits back to float64.
func Int64ToFloat64(i int64) float64 {
	return math.Float64frombits(uint64(i))
}
