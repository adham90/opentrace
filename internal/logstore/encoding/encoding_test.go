package encoding

import (
	"math"
	"testing"
)

// --- Delta encoding tests ---

func TestDeltaRoundTrip(t *testing.T) {
	values := []int64{1000, 1012, 1015, 1060, 1062, 1140}
	encoded := DeltaEncode(values)
	decoded, err := DeltaDecode(encoded, len(values))
	if err != nil {
		t.Fatalf("DeltaDecode: %v", err)
	}
	assertInt64Slice(t, "delta", values, decoded)
}

func TestDeltaMonotonic(t *testing.T) {
	// Typical timestamp pattern: monotonically increasing epoch ms
	values := make([]int64, 1000)
	base := int64(1743760800000)
	for i := range values {
		values[i] = base + int64(i)*12 // ~12ms apart
	}
	encoded := DeltaEncode(values)
	decoded, err := DeltaDecode(encoded, len(values))
	if err != nil {
		t.Fatalf("DeltaDecode: %v", err)
	}
	assertInt64Slice(t, "monotonic delta", values, decoded)

	// Should compress very well (small deltas)
	ratio := float64(len(values)*8) / float64(len(encoded))
	t.Logf("delta compression: %d values, %d raw bytes → %d encoded bytes (%.1fx)", len(values), len(values)*8, len(encoded), ratio)
	if ratio < 2 {
		t.Errorf("expected at least 2x compression for monotonic data, got %.1fx", ratio)
	}
}

func TestDeltaNonMonotonic(t *testing.T) {
	// Non-monotonic: some negative deltas (zigzag handles this)
	values := []int64{100, 200, 150, 300, 250, 400}
	encoded := DeltaEncode(values)
	decoded, err := DeltaDecode(encoded, len(values))
	if err != nil {
		t.Fatalf("DeltaDecode: %v", err)
	}
	assertInt64Slice(t, "non-monotonic delta", values, decoded)
}

func TestDeltaSingleValue(t *testing.T) {
	values := []int64{42}
	encoded := DeltaEncode(values)
	decoded, err := DeltaDecode(encoded, 1)
	if err != nil {
		t.Fatalf("DeltaDecode: %v", err)
	}
	assertInt64Slice(t, "single delta", values, decoded)
}

func TestDeltaEmpty(t *testing.T) {
	encoded := DeltaEncode(nil)
	if encoded != nil {
		t.Errorf("expected nil for empty delta encode, got %d bytes", len(encoded))
	}
}

func TestDeltaNegativeValues(t *testing.T) {
	values := []int64{-100, -50, 0, 50, 100}
	encoded := DeltaEncode(values)
	decoded, err := DeltaDecode(encoded, len(values))
	if err != nil {
		t.Fatalf("DeltaDecode: %v", err)
	}
	assertInt64Slice(t, "negative delta", values, decoded)
}

// --- Dictionary encoding tests ---

func TestDictRoundTrip(t *testing.T) {
	values := []string{"api", "worker", "api", "scheduler", "api", "worker", "api"}
	d := DictEncode(values)
	data := DictMarshal(d)
	d2, err := DictUnmarshal(data, len(values))
	if err != nil {
		t.Fatalf("DictUnmarshal: %v", err)
	}
	result := DictLookup(d2)
	assertStringSlice(t, "dict", values, result)
}

func TestDictWithEmptyStrings(t *testing.T) {
	values := []string{"api", "", "worker", "", "", "api"}
	d := DictEncode(values)
	data := DictMarshal(d)
	d2, err := DictUnmarshal(data, len(values))
	if err != nil {
		t.Fatalf("DictUnmarshal: %v", err)
	}
	result := DictLookup(d2)
	assertStringSlice(t, "dict empty", values, result)
}

func TestDictAllSame(t *testing.T) {
	values := make([]string, 1000)
	for i := range values {
		values[i] = "production"
	}
	d := DictEncode(values)
	if len(d.Dict) != 2 { // "" + "production"
		t.Errorf("expected 2 dict entries, got %d", len(d.Dict))
	}
	data := DictMarshal(d)
	d2, err := DictUnmarshal(data, len(values))
	if err != nil {
		t.Fatalf("DictUnmarshal: %v", err)
	}
	result := DictLookup(d2)
	assertStringSlice(t, "dict all same", values, result)
	t.Logf("dict all-same: %d values → %d encoded bytes", len(values), len(data))
}

func TestDictSingleValue(t *testing.T) {
	values := []string{"hello"}
	d := DictEncode(values)
	data := DictMarshal(d)
	d2, err := DictUnmarshal(data, 1)
	if err != nil {
		t.Fatalf("DictUnmarshal: %v", err)
	}
	result := DictLookup(d2)
	assertStringSlice(t, "dict single", values, result)
}

// --- Sparse encoding tests ---

func TestSparseStringsRoundTrip(t *testing.T) {
	values := []string{"trace-abc", "", "", "trace-def", "", "trace-ghi", "", "", "", ""}
	encoded := SparseStrings(values)
	decoded, err := UnsparseStrings(encoded, len(values))
	if err != nil {
		t.Fatalf("UnsparseStrings: %v", err)
	}
	assertStringSlice(t, "sparse strings", values, decoded)
}

func TestSparseStringsAllNull(t *testing.T) {
	values := make([]string, 100)
	encoded := SparseStrings(values)
	decoded, err := UnsparseStrings(encoded, len(values))
	if err != nil {
		t.Fatalf("UnsparseStrings: %v", err)
	}
	assertStringSlice(t, "sparse all null", values, decoded)
	t.Logf("sparse all-null: %d values → %d encoded bytes", len(values), len(encoded))
}

func TestSparseStringsAllPresent(t *testing.T) {
	values := []string{"a", "b", "c", "d", "e"}
	encoded := SparseStrings(values)
	decoded, err := UnsparseStrings(encoded, len(values))
	if err != nil {
		t.Fatalf("UnsparseStrings: %v", err)
	}
	assertStringSlice(t, "sparse all present", values, decoded)
}

func TestSparseInt64RoundTrip(t *testing.T) {
	v1, v2, v3 := int64(1243), int64(456), int64(789)
	values := []*int64{&v1, nil, nil, &v2, nil, &v3, nil, nil, nil, nil}
	encoded := SparseInt64(values)
	decoded, err := UnsparseInt64(encoded, len(values))
	if err != nil {
		t.Fatalf("UnsparseInt64: %v", err)
	}
	for i := range values {
		if values[i] == nil && decoded[i] != nil {
			t.Errorf("index %d: expected nil, got %d", i, *decoded[i])
		}
		if values[i] != nil && (decoded[i] == nil || *values[i] != *decoded[i]) {
			t.Errorf("index %d: expected %d, got %v", i, *values[i], decoded[i])
		}
	}
}

func TestSparseInt64AllNull(t *testing.T) {
	values := make([]*int64, 100)
	encoded := SparseInt64(values)
	decoded, err := UnsparseInt64(encoded, len(values))
	if err != nil {
		t.Fatalf("UnsparseInt64: %v", err)
	}
	for i, v := range decoded {
		if v != nil {
			t.Errorf("index %d: expected nil, got %d", i, *v)
		}
	}
}

func TestSparseBoolRoundTrip(t *testing.T) {
	tr, fa := true, false
	values := []*bool{&tr, nil, nil, &fa, nil, &tr, nil, nil, &fa, nil}
	encoded := SparseBool(values)
	decoded, err := UnsparseBool(encoded, len(values))
	if err != nil {
		t.Fatalf("UnsparseBool: %v", err)
	}
	for i := range values {
		if values[i] == nil && decoded[i] != nil {
			t.Errorf("index %d: expected nil, got %v", i, *decoded[i])
		}
		if values[i] != nil && (decoded[i] == nil || *values[i] != *decoded[i]) {
			t.Errorf("index %d: expected %v, got %v", i, *values[i], decoded[i])
		}
	}
}

func TestSparseBytesRoundTrip(t *testing.T) {
	body1 := []byte(`{"queries":[{"sql":"SELECT 1"}]}`)
	body2 := []byte(`{"timeline":[{"t":"db","ms":1.2}]}`)
	values := [][]byte{nil, nil, body1, nil, body2, nil, nil, nil, nil, nil}
	encoded := SparseBytes(values)
	decoded, err := UnsparseBytes(encoded, len(values))
	if err != nil {
		t.Fatalf("UnsparseBytes: %v", err)
	}
	for i := range values {
		if values[i] == nil && decoded[i] != nil {
			t.Errorf("index %d: expected nil, got %d bytes", i, len(decoded[i]))
		}
		if values[i] != nil {
			if decoded[i] == nil {
				t.Errorf("index %d: expected %d bytes, got nil", i, len(values[i]))
			} else if string(values[i]) != string(decoded[i]) {
				t.Errorf("index %d: content mismatch", i)
			}
		}
	}
}

// --- Bitpack encoding tests ---

func TestBitpackRoundTrip(t *testing.T) {
	// 3 bits per value: values 0-7
	indices := []uint8{1, 1, 0, 3, 1, 4, 1, 0, 2, 1}
	encoded := BitpackEncode(indices, 3)
	decoded, err := BitpackDecode(encoded)
	if err != nil {
		t.Fatalf("BitpackDecode: %v", err)
	}
	assertUint8Slice(t, "bitpack", indices, decoded)
}

func TestBitpack1Bit(t *testing.T) {
	indices := []uint8{0, 1, 1, 0, 1, 0, 0, 1, 1, 1, 0, 0}
	encoded := BitpackEncode(indices, 1)
	decoded, err := BitpackDecode(encoded)
	if err != nil {
		t.Fatalf("BitpackDecode: %v", err)
	}
	assertUint8Slice(t, "bitpack 1-bit", indices, decoded)
}

func TestBitpack8Bit(t *testing.T) {
	indices := []uint8{0, 127, 255, 42, 0, 200}
	encoded := BitpackEncode(indices, 8)
	decoded, err := BitpackDecode(encoded)
	if err != nil {
		t.Fatalf("BitpackDecode: %v", err)
	}
	assertUint8Slice(t, "bitpack 8-bit", indices, decoded)
}

func TestBitpackLargeSlice(t *testing.T) {
	// Simulate 50K level values (mostly "info" = 1)
	indices := make([]uint8, 50000)
	for i := range indices {
		switch {
		case i%100 < 62:
			indices[i] = 1 // info
		case i%100 < 78:
			indices[i] = 0 // debug
		case i%100 < 82:
			indices[i] = 2 // warn
		case i%100 < 86:
			indices[i] = 3 // error
		default:
			indices[i] = 4 // fatal
		}
	}
	encoded := BitpackEncode(indices, 3)
	decoded, err := BitpackDecode(encoded)
	if err != nil {
		t.Fatalf("BitpackDecode: %v", err)
	}
	assertUint8Slice(t, "bitpack large", indices, decoded)
	t.Logf("bitpack: %d values × 3 bits → %d bytes (%.1f bytes/value)", len(indices), len(encoded), float64(len(encoded))/float64(len(indices)))
}

func TestBitsNeeded(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{1, 1}, {2, 1}, {3, 2}, {4, 2}, {5, 3}, {8, 3}, {9, 4}, {16, 4}, {256, 8},
	}
	for _, tt := range tests {
		got := BitsNeeded(tt.n)
		if got != tt.want {
			t.Errorf("BitsNeeded(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

// --- Zstd block encoding tests ---

func TestZstdBlockStringsRoundTrip(t *testing.T) {
	values := []string{
		"POST /api/orders 201 1243ms",
		"Failed to charge customer: card declined",
		"Cache hit for user 42",
		"",
		"Connection timeout to redis:6379",
	}
	encoded := ZstdBlockEncodeStrings(values)
	decoded, err := ZstdBlockDecodeStrings(encoded, len(values))
	if err != nil {
		t.Fatalf("ZstdBlockDecodeStrings: %v", err)
	}
	assertStringSlice(t, "zstd block strings", values, decoded)
}

func TestZstdBlockStringsLarge(t *testing.T) {
	values := make([]string, 1000)
	for i := range values {
		values[i] = "Request completed: GET /api/users/123 200 45ms"
	}
	encoded := ZstdBlockEncodeStrings(values)
	decoded, err := ZstdBlockDecodeStrings(encoded, len(values))
	if err != nil {
		t.Fatalf("ZstdBlockDecodeStrings: %v", err)
	}
	assertStringSlice(t, "zstd block large", values, decoded)
	ratio := float64(len(values)*50) / float64(len(encoded))
	t.Logf("zstd block: %d identical strings → %d bytes (%.1fx compression)", len(values), len(encoded), ratio)
}

func TestZstdBlockInt64RoundTrip(t *testing.T) {
	values := []int64{1743760800000, 1743760800012, 1743760800015, 1743760800060}
	encoded := ZstdBlockEncodeInt64(values)
	decoded, err := ZstdBlockDecodeInt64(encoded, len(values))
	if err != nil {
		t.Fatalf("ZstdBlockDecodeInt64: %v", err)
	}
	assertInt64Slice(t, "zstd block int64", values, decoded)
}

// --- Varint encoding tests ---

func TestVarintSliceRoundTrip(t *testing.T) {
	values := []int64{0, 1, -1, 127, -128, 1000, -1000, math.MaxInt64, math.MinInt64}
	encoded := EncodeVarintSlice(values)
	decoded, err := DecodeVarintSlice(encoded, len(values))
	if err != nil {
		t.Fatalf("DecodeVarintSlice: %v", err)
	}
	assertInt64Slice(t, "varint slice", values, decoded)
}

func TestZigZag(t *testing.T) {
	tests := []int64{0, 1, -1, 2, -2, 100, -100, math.MaxInt64, math.MinInt64}
	for _, v := range tests {
		encoded := ZigZagEncode(v)
		decoded := ZigZagDecode(encoded)
		if decoded != v {
			t.Errorf("zigzag roundtrip(%d): got %d", v, decoded)
		}
	}
}

// --- Compress/Decompress tests ---

func TestCompressDecompress(t *testing.T) {
	data := []byte("hello world hello world hello world")
	compressed := Compress(data)
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if string(decompressed) != string(data) {
		t.Errorf("roundtrip mismatch: %q vs %q", data, decompressed)
	}
}

func TestCompressEmpty(t *testing.T) {
	compressed := Compress(nil)
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress empty: %v", err)
	}
	if len(decompressed) != 0 {
		t.Errorf("expected empty, got %d bytes", len(decompressed))
	}
}

// --- Helpers ---

func assertInt64Slice(t *testing.T, name string, want, got []int64) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: length mismatch: want %d, got %d", name, len(want), len(got))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("%s[%d]: want %d, got %d", name, i, want[i], got[i])
		}
	}
}

func assertStringSlice(t *testing.T, name string, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: length mismatch: want %d, got %d", name, len(want), len(got))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("%s[%d]: want %q, got %q", name, i, want[i], got[i])
		}
	}
}

func assertUint8Slice(t *testing.T, name string, want, got []uint8) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: length mismatch: want %d, got %d", name, len(want), len(got))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("%s[%d]: want %d, got %d", name, i, want[i], got[i])
		}
	}
}
