package encoding

import (
	"encoding/binary"
	"fmt"
	"testing"
)

// TestBitpackDecodeRejectsOversizedCount covers the unbounded-allocation bug: a
// corrupt chunk claiming 0xFFFFFFFF values used to allocate ~4GB before
// noticing there were no packed bytes to read.
func TestBitpackDecodeRejectsOversizedCount(t *testing.T) {
	valid := BitpackEncode([]uint8{1, 2, 3, 4}, 3)

	tests := []struct {
		name  string
		count uint32
	}{
		{"max_uint32", 0xFFFFFFFF},
		{"one_gig", 1 << 30},
		{"one_past_capacity", uint32(len(valid[5:])*8/3 + 1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			corrupt := append([]byte(nil), valid...)
			binary.LittleEndian.PutUint32(corrupt[1:5], tc.count)
			got, err := BitpackDecode(corrupt)
			if err == nil {
				t.Fatalf("expected an error for count %d, got %d decoded values", tc.count, len(got))
			}
		})
	}

	// The honest encoding must still decode.
	got, err := BitpackDecode(valid)
	if err != nil {
		t.Fatalf("BitpackDecode(valid): %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("valid decode: want 4 values, got %d", len(got))
	}
}

// TestDictEncodeBeyondUint16 covers the broken overflow guard: past 65535
// entries the dictionary index wrapped and collided with real entries, and
// DictMarshal wrote a wrapped entry count that corrupted the whole column.
func TestDictEncodeBeyondUint16(t *testing.T) {
	const n = MaxDictEntries + 1000
	values := make([]string, n)
	for i := range values {
		values[i] = fmt.Sprintf("v%d", i)
	}

	d := DictEncode(values)
	if len(d.Dict) > MaxDictEntries {
		t.Fatalf("dictionary grew past the cap: %d entries", len(d.Dict))
	}

	// No index may collide with a different value's index.
	seen := make(map[uint16]string, len(d.Dict))
	for i, idx := range d.Indices {
		if idx == 0 {
			continue // folded onto the null slot: lossy but not colliding
		}
		if prev, ok := seen[idx]; ok && prev != values[i] {
			t.Fatalf("index %d maps to both %q and %q", idx, prev, values[i])
		}
		seen[idx] = values[i]
	}

	// The column must still round-trip, and every non-folded value must survive.
	d2, err := DictUnmarshal(DictMarshal(d), len(values), LenExtended)
	if err != nil {
		t.Fatalf("DictUnmarshal: %v", err)
	}
	got := DictLookup(d2)
	kept := 0
	for i, v := range got {
		if v == "" {
			continue
		}
		if v != values[i] {
			t.Fatalf("value %d: want %q, got %q", i, values[i], v)
		}
		kept++
	}
	if kept != MaxDictEntries-1 {
		t.Fatalf("want %d values preserved, got %d", MaxDictEntries-1, kept)
	}
}

func TestDictEncodeLimitNarrowIndexWidth(t *testing.T) {
	values := make([]string, 1000)
	for i := range values {
		values[i] = fmt.Sprintf("level-%d", i)
	}
	d := DictEncodeLimit(values, 256)
	if len(d.Dict) > 256 {
		t.Fatalf("dictionary grew past the limit: %d entries", len(d.Dict))
	}
	if bits := BitsNeeded(len(d.Dict)); bits > 8 {
		t.Fatalf("dictionary needs %d bits, more than a bitpacked column can hold", bits)
	}
	for _, idx := range d.Indices {
		if idx > 255 {
			t.Fatalf("index %d does not fit in a uint8", idx)
		}
	}
}

// TestDecompressLargeBlock covers the encoder/decoder asymmetry: the decoder
// used to refuse anything over 64MB while the encoder had no limit, so a busy
// hour's message column sealed fine and then decoded to nothing forever.
func TestDecompressLargeBlock(t *testing.T) {
	const size = 80 << 20 // comfortably past the old 64MB decoder ceiling
	src := make([]byte, size)
	for i := range src {
		src[i] = byte(i % 251)
	}

	got, err := Decompress(Compress(src))
	if err != nil {
		t.Fatalf("Decompress(%d bytes): %v", size, err)
	}
	if len(got) != size {
		t.Fatalf("want %d bytes back, got %d", size, len(got))
	}
}

// TestSparseInt64RejectsTruncated makes sure a truncated sparse column errors
// instead of panicking on an out-of-range slice.
func TestSparseInt64RejectsTruncated(t *testing.T) {
	// A bitmap claiming 8 non-null values over a payload holding only 2.
	payload := PutVarint(nil, 7)
	payload = PutVarint(payload, 9)
	corrupt := marshalSparse(8, []byte{0xFF}, Compress(payload))

	if _, err := UnsparseInt64(corrupt, 8); err == nil {
		t.Fatal("expected an error decoding more values than were encoded")
	}
}
