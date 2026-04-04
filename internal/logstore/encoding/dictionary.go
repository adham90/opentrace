package encoding

import (
	"encoding/binary"
	"fmt"
)

// --- Encoding 2: Dictionary + Zstd ---
// For low-cardinality string columns (service, env, event_type, method, status, controller, action).
// Builds a string→uint16 mapping, stores uint16 indices, zstd compresses.

// DictEncoded holds the result of dictionary encoding.
type DictEncoded struct {
	Dict    []string // ordered dictionary: index → string
	Indices []uint16 // per-entry dictionary index
}

// DictEncode builds a dictionary from string values and returns indices.
// Null/empty strings are stored as index 0 with dict[0] = "".
func DictEncode(values []string) *DictEncoded {
	dict := make(map[string]uint16)
	order := make([]string, 0, 16)

	// Reserve index 0 for empty string
	dict[""] = 0
	order = append(order, "")

	indices := make([]uint16, len(values))
	for i, v := range values {
		idx, ok := dict[v]
		if !ok {
			idx = uint16(len(order))
			if int(idx) >= 65535 {
				// Fallback: too many unique values for uint16.
				// In practice this shouldn't happen for dict-encoded columns.
				idx = 0
			}
			dict[v] = idx
			order = append(order, v)
		}
		indices[i] = idx
	}

	return &DictEncoded{Dict: order, Indices: indices}
}

// DictMarshal serializes a DictEncoded to bytes + zstd.
// Format: [2 bytes dict_len] [dict entries: 2-byte len + string]... [zstd(uint16 indices)]
func DictMarshal(d *DictEncoded) []byte {
	// Serialize dictionary
	dictBuf := make([]byte, 0, len(d.Dict)*16)
	dictBuf = binary.LittleEndian.AppendUint16(dictBuf, uint16(len(d.Dict)))
	for _, s := range d.Dict {
		dictBuf = binary.LittleEndian.AppendUint16(dictBuf, uint16(len(s)))
		dictBuf = append(dictBuf, s...)
	}

	// Serialize indices as raw uint16 LE, then compress
	idxBuf := make([]byte, len(d.Indices)*2)
	for i, idx := range d.Indices {
		binary.LittleEndian.PutUint16(idxBuf[i*2:], idx)
	}
	compressedIdx := Compress(idxBuf)

	// [4 bytes dict_size] [dict_bytes] [compressed_indices]
	result := make([]byte, 0, 4+len(dictBuf)+len(compressedIdx))
	result = binary.LittleEndian.AppendUint32(result, uint32(len(dictBuf)))
	result = append(result, dictBuf...)
	result = append(result, compressedIdx...)
	return result
}

// DictUnmarshal deserializes DictEncoded from bytes.
func DictUnmarshal(data []byte, count int) (*DictEncoded, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("dict data too short: %d bytes", len(data))
	}

	dictSize := int(binary.LittleEndian.Uint32(data[:4]))
	if 4+dictSize > len(data) {
		return nil, fmt.Errorf("dict size %d exceeds data length %d", dictSize, len(data))
	}

	// Parse dictionary
	dictBuf := data[4 : 4+dictSize]
	if len(dictBuf) < 2 {
		return nil, fmt.Errorf("dict buffer too short")
	}
	dictLen := int(binary.LittleEndian.Uint16(dictBuf[:2]))
	offset := 2

	dict := make([]string, 0, dictLen)
	for i := 0; i < dictLen; i++ {
		if offset+2 > len(dictBuf) {
			return nil, fmt.Errorf("dict entry %d: truncated length", i)
		}
		sLen := int(binary.LittleEndian.Uint16(dictBuf[offset:]))
		offset += 2
		if offset+sLen > len(dictBuf) {
			return nil, fmt.Errorf("dict entry %d: truncated string", i)
		}
		dict = append(dict, string(dictBuf[offset:offset+sLen]))
		offset += sLen
	}

	// Decompress indices
	compressedIdx := data[4+dictSize:]
	rawIdx, err := Decompress(compressedIdx)
	if err != nil {
		return nil, fmt.Errorf("decompress dict indices: %w", err)
	}

	indices := make([]uint16, count)
	for i := 0; i < count && i*2+1 < len(rawIdx); i++ {
		indices[i] = binary.LittleEndian.Uint16(rawIdx[i*2:])
	}

	return &DictEncoded{Dict: dict, Indices: indices}, nil
}

// DictLookup resolves dictionary indices back to strings.
func DictLookup(d *DictEncoded) []string {
	result := make([]string, len(d.Indices))
	for i, idx := range d.Indices {
		if int(idx) < len(d.Dict) {
			result[i] = d.Dict[idx]
		}
	}
	return result
}
