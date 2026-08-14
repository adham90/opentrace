package encoding

import (
	"encoding/binary"
	"fmt"
)

// --- Encoding 5: Bitpack ---
// For small enum columns (level: ≤8 values → 3 bits per entry).
// Packs N-bit indices tightly. Combined with dictionary for the string→index mapping.

// BitpackEncode packs uint8 indices into N bits per value.
// bitsPerValue must be 1-8.
// Returns: [1 byte bitsPerValue] [4 bytes count] [packed bits]
func BitpackEncode(indices []uint8, bitsPerValue int) []byte {
	if bitsPerValue < 1 || bitsPerValue > 8 {
		panic(fmt.Sprintf("bitpack: bitsPerValue must be 1-8, got %d", bitsPerValue))
	}

	totalBits := len(indices) * bitsPerValue
	packedSize := (totalBits + 7) / 8
	packed := make([]byte, packedSize)

	bitPos := 0
	mask := uint8((1 << bitsPerValue) - 1)
	for _, idx := range indices {
		val := idx & mask
		byteIdx := bitPos / 8
		bitOffset := bitPos % 8

		// Write value across byte boundary if needed
		packed[byteIdx] |= val << bitOffset
		if bitOffset+bitsPerValue > 8 && byteIdx+1 < len(packed) {
			packed[byteIdx+1] |= val >> (8 - bitOffset)
		}
		bitPos += bitsPerValue
	}

	// [1 byte bitsPerValue] [4 bytes count] [packed]
	result := make([]byte, 0, 5+len(packed))
	result = append(result, byte(bitsPerValue))
	result = binary.LittleEndian.AppendUint32(result, uint32(len(indices)))
	result = append(result, packed...)
	return result
}

// BitpackDecode unpacks N-bit packed indices.
func BitpackDecode(data []byte) ([]uint8, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("bitpack data too short: %d bytes", len(data))
	}

	bitsPerValue := int(data[0])
	if bitsPerValue < 1 || bitsPerValue > 8 {
		return nil, fmt.Errorf("bitpack: invalid bitsPerValue %d", bitsPerValue)
	}
	count := int(binary.LittleEndian.Uint32(data[1:5]))
	packed := data[5:]

	// The count comes straight off disk. Without a bound, a corrupt or crafted
	// chunk claiming 0xFFFFFFFF values would allocate ~4GB here (and far more in
	// the caller resolving indices through a dictionary) and OOM the process.
	// The packed payload is self-describing: count values of bitsPerValue bits
	// cannot occupy more than len(packed) bytes.
	maxCount := len(packed) * 8 / bitsPerValue
	if count < 0 || count > maxCount {
		return nil, fmt.Errorf("bitpack: count %d exceeds %d values encodable in %d packed bytes at %d bits", count, maxCount, len(packed), bitsPerValue)
	}

	mask := uint8((1 << bitsPerValue) - 1)
	result := make([]uint8, count)
	bitPos := 0
	for i := range count {
		byteIdx := bitPos / 8
		bitOffset := bitPos % 8

		if byteIdx >= len(packed) {
			break
		}

		val := packed[byteIdx] >> bitOffset
		if bitOffset+bitsPerValue > 8 && byteIdx+1 < len(packed) {
			val |= packed[byteIdx+1] << (8 - bitOffset)
		}
		result[i] = val & mask
		bitPos += bitsPerValue
	}

	return result, nil
}

// BitsNeeded returns the minimum number of bits to represent n distinct values.
func BitsNeeded(n int) int {
	if n <= 1 {
		return 1
	}
	bits := 0
	v := n - 1
	for v > 0 {
		bits++
		v >>= 1
	}
	return bits
}
