package encoding

import (
	"encoding/binary"
	"fmt"
	"log/slog"
)

// --- Encoding 2: Dictionary + Zstd ---
// For low-cardinality string columns (service, env, event_type, method, status, controller, action).
// Builds a string→uint16 mapping, stores uint16 indices, zstd compresses.

// MaxDictEntries is the largest dictionary DictMarshal can represent: the
// serialized entry count is a uint16, so 65535 entries (indices 0..65534) is
// the ceiling. Beyond it the count would wrap and the whole column would decode
// to garbage, so DictEncode stops growing the dictionary instead.
const MaxDictEntries = 65535

// DictEncoded holds the result of dictionary encoding.
type DictEncoded struct {
	Dict    []string // ordered dictionary: index → string
	Indices []uint16 // per-entry dictionary index
}

// DictEncode builds a dictionary from string values and returns indices.
// Null/empty strings are stored as index 0 with dict[0] = "".
func DictEncode(values []string) *DictEncoded {
	return DictEncodeLimit(values, MaxDictEntries)
}

// DictEncodeLimit is DictEncode with an explicit cap on dictionary size. Once
// the cap is reached, further distinct values map to index 0 (the empty string)
// and the dictionary stops growing — lossy, but bounded and never misframed.
// Callers whose index width is narrower than uint16 (bitpacked columns) pass a
// smaller limit.
func DictEncodeLimit(values []string, limit int) *DictEncoded {
	if limit < 1 || limit > MaxDictEntries {
		limit = MaxDictEntries
	}

	dict := make(map[string]uint16)
	order := make([]string, 0, 16)

	// Reserve index 0 for empty string
	dict[""] = 0
	order = append(order, "")

	indices := make([]uint16, len(values))
	for i, v := range values {
		idx, ok := dict[v]
		if !ok {
			if len(order) >= limit {
				// Dictionary full: fold the value onto index 0 without growing
				// the dictionary, so indices stay unique and the entry count
				// stays representable.
				indices[i] = 0
				continue
			}
			idx = uint16(len(order))
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
	// Defensive: the entry count is a uint16 on disk. DictEncode never exceeds
	// MaxDictEntries; a hand-built DictEncoded might, and writing a wrapped count
	// would corrupt the entire column. Drop the overflow instead — indices past
	// the end resolve to "" in DictLookup.
	entries := d.Dict
	if len(entries) > MaxDictEntries {
		slog.Error("dictionary exceeds max entries, truncating",
			"entries", len(entries), "max", MaxDictEntries)
		entries = entries[:MaxDictEntries]
	}

	// Serialize dictionary
	dictBuf := make([]byte, 0, len(entries)*16)
	dictBuf = binary.LittleEndian.AppendUint16(dictBuf, uint16(len(entries)))
	for _, s := range entries {
		dictBuf = AppendLenString(dictBuf, s)
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

// DictUnmarshal deserializes DictEncoded from bytes using the given length
// framing (LenUint16 for chunk v1 files, LenExtended for v2+).
func DictUnmarshal(data []byte, count int, f LenFormat) (*DictEncoded, error) {
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
		var s string
		var err error
		s, offset, err = f.ReadString(dictBuf, offset)
		if err != nil {
			return nil, fmt.Errorf("dict entry %d: %w", i, err)
		}
		dict = append(dict, s)
	}

	// Decompress indices
	compressedIdx := data[4+dictSize:]
	// Indices are raw uint16s: exactly two bytes per value.
	rawIdx, err := DecompressBounded(compressedIdx, DecodeCeiling(count, 2))
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
