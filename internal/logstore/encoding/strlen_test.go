package encoding

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

func bigString(n int, seed byte) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a' + byte(i%26) + seed%3
	}
	return string(b)
}

// sizes exercises the uint16 boundary in both directions.
var framingSizes = []int{0, 1, 100, 65534, 65535, 65536, 70000, 200000}

// TestLengthFramingRoundTrip covers the critical misframing bug: the encoders
// used to write uint16(len(v)) but append all the bytes, so any value over
// 65535 bytes shifted every later value in the column.
func TestLengthFramingRoundTrip(t *testing.T) {
	for _, n := range framingSizes {
		t.Run(fmt.Sprintf("bytes=%d", n), func(t *testing.T) {
			// A big value sandwiched between small ones: misframing shows up as
			// a corrupted or missing neighbour, not just a bad big value.
			values := []string{"before", bigString(n, 0), "after"}

			t.Run("zstdblock", func(t *testing.T) {
				got, err := ZstdBlockDecodeStrings(ZstdBlockEncodeStrings(values), len(values), LenExtended)
				if err != nil {
					t.Fatalf("ZstdBlockDecodeStrings: %v", err)
				}
				assertStrings(t, values, got)
			})

			t.Run("sparse", func(t *testing.T) {
				if n == 0 {
					t.Skip("empty string is null in the sparse encoding")
				}
				got, err := UnsparseStrings(SparseStrings(values), len(values), LenExtended)
				if err != nil {
					t.Fatalf("UnsparseStrings: %v", err)
				}
				assertStrings(t, values, got)
			})

			t.Run("dictionary", func(t *testing.T) {
				d := DictEncode(values)
				d2, err := DictUnmarshal(DictMarshal(d), len(values), LenExtended)
				if err != nil {
					t.Fatalf("DictUnmarshal: %v", err)
				}
				assertStrings(t, values, DictLookup(d2))
			})
		})
	}
}

func assertStrings(t *testing.T, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("length: want %d values, got %d", len(want), len(got))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("value %d: want %d bytes (%q…), got %d bytes (%q…)",
				i, len(want[i]), trunc(want[i]), len(got[i]), trunc(got[i]))
		}
	}
}

func trunc(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

// TestLegacyUint16FramingReadable proves chunk v1 files (bare uint16 prefixes)
// still decode after the format change.
func TestLegacyUint16FramingReadable(t *testing.T) {
	values := []string{"alpha", "", strings.Repeat("z", 1000)}

	var raw []byte
	for _, v := range values {
		raw = binary.LittleEndian.AppendUint16(raw, uint16(len(v)))
		raw = append(raw, v...)
	}

	got, err := ZstdBlockDecodeStrings(Compress(raw), len(values), LenUint16)
	if err != nil {
		t.Fatalf("decode legacy block: %v", err)
	}
	assertStrings(t, values, got)
}

// TestLenFormatRejectsTruncated makes sure a short buffer is an error, never a
// fabricated value.
func TestLenFormatRejectsTruncated(t *testing.T) {
	buf := AppendLenString(nil, "hello world")
	for _, cut := range []int{1, 2, 5, len(buf) - 1} {
		if _, _, err := LenExtended.ReadString(buf[:cut], 0); err == nil {
			t.Errorf("cut at %d: expected an error, got none", cut)
		}
	}
}

func TestLenFormatRejectsAbsurdLength(t *testing.T) {
	buf := binary.LittleEndian.AppendUint16(nil, extendedLenMarker)
	buf = binary.LittleEndian.AppendUint32(buf, 0xFFFFFFFF)
	if _, _, err := LenExtended.ReadValue(buf, 0); err == nil {
		t.Fatal("expected an error for a length past MaxValueBytes, got none")
	}
}
