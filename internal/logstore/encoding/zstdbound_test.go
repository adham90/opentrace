package encoding

import (
	"strings"
	"testing"
)

// TestDecodeCeiling checks the derived per-column bounds.
func TestDecodeCeiling(t *testing.T) {
	cases := []struct {
		count, perValue, want int
	}{
		{0, 8, minDecodeCeiling},                    // empty column still tolerates framing
		{10, 8, minDecodeCeiling},                   // floor applies to tiny columns
		{1 << 20, 8, 8 << 20},                       // fixed-width: tight
		{50000, MaxColumnValueBytes, MaxBlockBytes}, // variable-length: clamped
	}
	for _, c := range cases {
		if got := DecodeCeiling(c.count, c.perValue); got != c.want {
			t.Fatalf("DecodeCeiling(%d, %d) = %d, want %d", c.count, c.perValue, got, c.want)
		}
	}
}

// TestDecompressionBombRejected: readColumnRaw only bounds the *compressed*
// column against the file size, so a ~KB column used to be allowed to expand
// toward the global 2 GiB decoder ceiling inside a search goroutine. Every
// column decode now runs under a ceiling derived from its own value count.
func TestDecompressionBombRejected(t *testing.T) {
	const bombBytes = 64 << 20 // expands to 64 MiB from a few hundred bytes
	bomb := Compress(make([]byte, bombBytes))
	if len(bomb) > 1<<16 {
		t.Fatalf("test bomb is not compressed enough to be interesting: %d bytes", len(bomb))
	}

	// A 10-value int64 column can hold 80 bytes; 64 MiB must be refused.
	if _, err := ZstdBlockDecodeInt64(bomb, 10); err == nil {
		t.Fatal("64 MiB expansion accepted for a 10-value int64 column")
	}
	if _, err := DeltaDecode(append(make([]byte, 8), bomb...), 10); err == nil {
		t.Fatal("64 MiB expansion accepted for a 10-value delta column")
	}
	if _, err := DecompressBounded(bomb, 1<<20); err == nil {
		t.Fatal("64 MiB expansion accepted under a 1 MiB ceiling")
	}
	// The same bytes are fine when the column legitimately could be that big.
	if _, err := DecompressBounded(bomb, bombBytes); err != nil {
		t.Fatalf("legitimate large column rejected: %v", err)
	}
}

// TestLargeLegitimateColumnStillDecodes: the ceiling must not undo the reason
// the global cap was raised — real columns bigger than the old 64 MB limit.
func TestLargeLegitimateColumnStillDecodes(t *testing.T) {
	const rows = 200
	value := strings.Repeat("x", 400<<10) // 400 KiB per row → 80 MiB column
	values := make([]string, rows)
	for i := range values {
		values[i] = value
	}
	encoded := ZstdBlockEncodeStrings(values)
	decoded, err := ZstdBlockDecodeStrings(encoded, rows, LenExtended)
	if err != nil {
		t.Fatalf("decode large string column: %v", err)
	}
	if len(decoded) != rows || decoded[rows-1] != value {
		t.Fatal("large string column did not round-trip")
	}
}

// TestDecoderBucket keeps the pooled-decoder sizing honest.
func TestDecoderBucket(t *testing.T) {
	cases := map[int]int{
		1:                    decoderBucketMin,
		decoderBucketMin:     decoderBucketMin,
		decoderBucketMin + 1: decoderBucketMin * 2,
		MaxBlockBytes:        MaxBlockBytes,
		MaxBlockBytes * 2:    MaxBlockBytes,
	}
	for in, want := range cases {
		if got := decoderBucket(in); got != want {
			t.Fatalf("decoderBucket(%d) = %d, want %d", in, got, want)
		}
	}
}
