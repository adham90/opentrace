package encoding

import (
	"encoding/binary"
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

// ZigZagEncode converts a signed int64 to an unsigned uint64 using zigzag encoding.
// This makes small negative numbers small unsigned numbers (good for varint).
func ZigZagEncode(v int64) uint64 {
	return uint64((v << 1) ^ (v >> 63))
}

// ZigZagDecode converts a zigzag-encoded uint64 back to int64.
func ZigZagDecode(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}
