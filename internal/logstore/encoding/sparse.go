package encoding

import (
	"encoding/binary"
	"fmt"
)

// --- Encoding 3: Sparse + Zstd ---
// For columns that are mostly null (trace_id, span_id, request_id, etc.).
// Stores: null bitmap (1 bit/entry) + packed non-null values + zstd.

// SparseStrings encodes a slice of strings where empty string = null.
// Format: [4 bytes non_null_count] [bitmap bytes] [zstd(length-prefixed non-null strings)]
func SparseStrings(values []string) []byte {
	bitmap, nonNullCount := buildBitmap(values, func(v string) bool { return v != "" })

	// Pack non-null values
	var buf []byte
	for _, v := range values {
		if v != "" {
			buf = AppendLenString(buf, v)
		}
	}
	compressed := Compress(buf)

	return marshalSparse(nonNullCount, bitmap, compressed)
}

// UnsparseStrings decodes sparse string column using the given length framing
// (LenUint16 for chunk v1 files, LenExtended for v2+).
func UnsparseStrings(data []byte, count int, f LenFormat) ([]string, error) {
	nonNullCount, bitmap, compressedValues, err := unmarshalSparse(data)
	if err != nil {
		return nil, err
	}

	var raw []byte
	if nonNullCount > 0 {
		raw, err = DecompressBounded(compressedValues, DecodeCeiling(count, MaxColumnValueBytes+MaxLenFramingBytes))
		if err != nil {
			return nil, fmt.Errorf("decompress sparse strings: %w", err)
		}
	}

	result := make([]string, count)
	offset := 0
	for i := 0; i < count; i++ {
		if getBit(bitmap, i) {
			var v string
			v, offset, err = f.ReadString(raw, offset)
			if err != nil {
				return nil, fmt.Errorf("sparse string %d: %w", i, err)
			}
			result[i] = v
		}
	}
	return result, nil
}

// SparseInt64 encodes a slice of *int64 where nil = null.
// Uses varint encoding for non-null values.
func SparseInt64(values []*int64) []byte {
	bitmap, nonNullCount := buildBitmapPtr(values)

	var buf []byte
	for _, v := range values {
		if v != nil {
			buf = PutVarint(buf, *v)
		}
	}
	compressed := Compress(buf)

	return marshalSparse(nonNullCount, bitmap, compressed)
}

// UnsparseInt64 decodes sparse int64 column.
func UnsparseInt64(data []byte, count int) ([]*int64, error) {
	nonNullCount, bitmap, compressedValues, err := unmarshalSparse(data)
	if err != nil {
		return nil, err
	}

	var raw []byte
	if nonNullCount > 0 {
		raw, err = DecompressBounded(compressedValues, DecodeCeiling(count, MaxVarintBytes))
		if err != nil {
			return nil, fmt.Errorf("decompress sparse int64: %w", err)
		}
	}

	result := make([]*int64, count)
	offset := 0
	for i := 0; i < count; i++ {
		if getBit(bitmap, i) {
			if offset >= len(raw) {
				return nil, fmt.Errorf("sparse int64 %d: truncated at offset %d", i, offset)
			}
			v, n := binary.Varint(raw[offset:])
			if n <= 0 {
				return nil, fmt.Errorf("sparse int64 %d: invalid varint", i)
			}
			val := v
			result[i] = &val
			offset += n
		}
	}
	return result, nil
}

// SparseBool encodes a slice of *bool where nil = null.
// Non-null values stored as a second bitmap.
func SparseBool(values []*bool) []byte {
	nullBitmap, nonNullCount := buildBitmapPtrBool(values)

	// Build value bitmap for non-null entries
	valueBitmapSize := (nonNullCount + 7) / 8
	valueBitmap := make([]byte, valueBitmapSize)
	idx := 0
	for _, v := range values {
		if v != nil {
			if *v {
				valueBitmap[idx/8] |= 1 << (idx % 8)
			}
			idx++
		}
	}

	return marshalSparse(nonNullCount, nullBitmap, valueBitmap)
}

// UnsparseBool decodes sparse bool column.
func UnsparseBool(data []byte, count int) ([]*bool, error) {
	_, nullBitmap, valueBitmap, err := unmarshalSparse(data)
	if err != nil {
		return nil, err
	}

	result := make([]*bool, count)
	idx := 0
	for i := 0; i < count; i++ {
		if getBit(nullBitmap, i) {
			v := getBit(valueBitmap, idx)
			result[i] = &v
			idx++
		}
	}
	return result, nil
}

// SparseBytes encodes a slice of []byte where nil = null.
// Used for body column (already compressed blobs).
func SparseBytes(values [][]byte) []byte {
	bitmap, nonNullCount := buildBitmapBytes(values)

	var buf []byte
	for _, v := range values {
		if v != nil {
			buf = binary.LittleEndian.AppendUint32(buf, uint32(len(v)))
			buf = append(buf, v...)
		}
	}
	// Don't double-compress: body blobs are already zstd compressed
	// But the length-prefix overhead benefits from compression
	compressed := Compress(buf)

	return marshalSparse(nonNullCount, bitmap, compressed)
}

// UnsparseBytes decodes sparse byte slice column.
func UnsparseBytes(data []byte, count int) ([][]byte, error) {
	nonNullCount, bitmap, compressedValues, err := unmarshalSparse(data)
	if err != nil {
		return nil, err
	}

	var raw []byte
	if nonNullCount > 0 {
		raw, err = DecompressBounded(compressedValues, DecodeCeiling(count, MaxColumnValueBytes+MaxLenFramingBytes))
		if err != nil {
			return nil, fmt.Errorf("decompress sparse bytes: %w", err)
		}
	}

	result := make([][]byte, count)
	offset := 0
	for i := 0; i < count; i++ {
		if getBit(bitmap, i) {
			if offset+4 > len(raw) {
				return nil, fmt.Errorf("sparse bytes %d: truncated length", i)
			}
			bLen := int(binary.LittleEndian.Uint32(raw[offset:]))
			offset += 4
			if offset+bLen > len(raw) {
				return nil, fmt.Errorf("sparse bytes %d: truncated data", i)
			}
			val := make([]byte, bLen)
			copy(val, raw[offset:offset+bLen])
			result[i] = val
			offset += bLen
		}
	}
	return result, nil
}

// --- Bitmap helpers ---

func buildBitmap[T any](values []T, isPresent func(T) bool) (bitmap []byte, nonNullCount int) {
	bitmapSize := (len(values) + 7) / 8
	bitmap = make([]byte, bitmapSize)
	for i, v := range values {
		if isPresent(v) {
			bitmap[i/8] |= 1 << (i % 8)
			nonNullCount++
		}
	}
	return
}

func buildBitmapPtr(values []*int64) (bitmap []byte, nonNullCount int) {
	bitmapSize := (len(values) + 7) / 8
	bitmap = make([]byte, bitmapSize)
	for i, v := range values {
		if v != nil {
			bitmap[i/8] |= 1 << (i % 8)
			nonNullCount++
		}
	}
	return
}

func buildBitmapPtrBool(values []*bool) (bitmap []byte, nonNullCount int) {
	bitmapSize := (len(values) + 7) / 8
	bitmap = make([]byte, bitmapSize)
	for i, v := range values {
		if v != nil {
			bitmap[i/8] |= 1 << (i % 8)
			nonNullCount++
		}
	}
	return
}

func buildBitmapBytes(values [][]byte) (bitmap []byte, nonNullCount int) {
	bitmapSize := (len(values) + 7) / 8
	bitmap = make([]byte, bitmapSize)
	for i, v := range values {
		if v != nil {
			bitmap[i/8] |= 1 << (i % 8)
			nonNullCount++
		}
	}
	return
}

func getBit(bitmap []byte, i int) bool {
	if i/8 >= len(bitmap) {
		return false
	}
	return bitmap[i/8]&(1<<(i%8)) != 0
}

// marshalSparse packages sparse data: [4 bytes nonNullCount] [4 bytes bitmapSize] [bitmap] [values]
func marshalSparse(nonNullCount int, bitmap, values []byte) []byte {
	result := make([]byte, 0, 8+len(bitmap)+len(values))
	result = binary.LittleEndian.AppendUint32(result, uint32(nonNullCount))
	result = binary.LittleEndian.AppendUint32(result, uint32(len(bitmap)))
	result = append(result, bitmap...)
	result = append(result, values...)
	return result
}

// unmarshalSparse extracts nonNullCount, bitmap, and value data from sparse-encoded bytes.
func unmarshalSparse(data []byte) (nonNullCount int, bitmap, values []byte, err error) {
	if len(data) < 8 {
		return 0, nil, nil, fmt.Errorf("sparse data too short: %d bytes", len(data))
	}
	nonNullCount = int(binary.LittleEndian.Uint32(data[:4]))
	bitmapSize := int(binary.LittleEndian.Uint32(data[4:8]))
	if 8+bitmapSize > len(data) {
		return 0, nil, nil, fmt.Errorf("bitmap size %d exceeds data length %d", bitmapSize, len(data))
	}
	bitmap = data[8 : 8+bitmapSize]
	values = data[8+bitmapSize:]
	return nonNullCount, bitmap, values, nil
}
