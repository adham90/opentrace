package encoding

import (
	"fmt"
	"log/slog"
	"runtime"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// MaxBlockBytes is the largest uncompressed block the codec supports. It is the
// absolute decoder ceiling *and* the encoder's contract: anything larger than
// this seals fine but can never be read back, so Compress reports a violation
// loudly instead of producing a write-only block. A chunk holds up to 50k rows,
// so this leaves ~40KB per row for the biggest single-block column (message).
//
// It is deliberately *not* the ceiling any single column decode runs under —
// see DecodeCeiling. Bounding only the compressed bytes against the file size
// let a corrupt or crafted 2 MB column expand toward 2 GiB inside a search
// goroutine; every column decode now runs under a ceiling derived from its own
// value count.
const MaxBlockBytes = 2 << 30 // 2 GiB

// Per-value ceilings used to derive a column's decoded-size bound.
const (
	// MaxColumnValueBytes is the largest single value that can reach a chunk:
	// the WAL truncates every field to 1 MiB before it is sealed. (The larger
	// MaxValueBytes in strlen.go bounds the *framing*, not what the pipeline
	// actually admits.)
	MaxColumnValueBytes = 1 << 20
	// MaxVarintBytes is the widest encoding of one varint/zigzag-varint value.
	MaxVarintBytes = 10
	// MaxLenFramingBytes covers the length prefix stored alongside a
	// variable-length value.
	MaxLenFramingBytes = 8
	// minDecodeCeiling floors the derived ceiling so a tiny (or empty) column
	// still tolerates framing overhead.
	minDecodeCeiling = 1 << 16
	// decoderBucketMin is the smallest max-memory a pooled decoder is built
	// with; ceilings are rounded up to a power of two from here so the pool
	// holds at most a couple of dozen decoders.
	decoderBucketMin = 1 << 20
)

// DecodeCeiling returns the largest decoded size a column of count values can
// legitimately reach, given a per-value maximum. Fixed-width columns
// (timestamps, durations, dictionary indices) end up with a tight bound; only
// the genuinely variable-length columns keep a large one, and even those are
// capped at MaxBlockBytes.
func DecodeCeiling(count, perValueBytes int) int {
	if count <= 0 || perValueBytes <= 0 {
		return minDecodeCeiling
	}
	if count > MaxBlockBytes/perValueBytes {
		return MaxBlockBytes
	}
	if n := count * perValueBytes; n > minDecodeCeiling {
		return n
	}
	return minDecodeCeiling
}

// Shared zstd encoder — allocating these is expensive.
var (
	encoderOnce sync.Once
	sharedEnc   *zstd.Encoder
	// decoderPool holds one decoder per max-memory bucket. A decoder's ceiling
	// is fixed at construction, so a bounded decode picks the smallest bucket
	// that still admits the column's legitimate size.
	decoderPool sync.Map // int bucket -> *zstd.Decoder
)

// codecConcurrency sizes the pooled block states inside the shared encoder and
// decoder. DecodeAll/EncodeAll take one state from a channel of this size, so
// concurrency 1 serialized every compression call in the process: dozens of
// column reads per chunk across all concurrent searches queued behind a single
// state. Size it to the available CPUs instead.
func codecConcurrency() int {
	n := runtime.GOMAXPROCS(0)
	if n < 1 {
		return 1
	}
	return n
}

func zstdEncoder() *zstd.Encoder {
	encoderOnce.Do(func() {
		var err error
		sharedEnc, err = zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstd.SpeedDefault),
			zstd.WithEncoderConcurrency(codecConcurrency()),
		)
		if err != nil {
			panic("zstd encoder init: " + err.Error())
		}
	})
	return sharedEnc
}

// decoderBucket rounds a ceiling up to the decoder pool's bucket size.
func decoderBucket(max int) int {
	if max >= MaxBlockBytes {
		return MaxBlockBytes
	}
	b := decoderBucketMin
	for b < max {
		b <<= 1
	}
	return b
}

// boundedDecoder returns a decoder that refuses to expand a frame past max.
func boundedDecoder(max int) *zstd.Decoder {
	bucket := decoderBucket(max)
	if d, ok := decoderPool.Load(bucket); ok {
		return d.(*zstd.Decoder)
	}
	d, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(codecConcurrency()),
		zstd.WithDecoderMaxMemory(uint64(bucket)),
	)
	if err != nil {
		panic("zstd decoder init: " + err.Error())
	}
	actual, loaded := decoderPool.LoadOrStore(bucket, d)
	if loaded {
		d.Close() // lost the race; keep the decoder already published
	}
	return actual.(*zstd.Decoder)
}

// Compress compresses data using zstd.
func Compress(src []byte) []byte {
	if len(src) > MaxBlockBytes {
		// Writing this would produce a block the decoder refuses to expand.
		slog.Error("zstd block exceeds max decompressible size; it will not be readable",
			"bytes", len(src), "max", MaxBlockBytes)
	}
	return zstdEncoder().EncodeAll(src, make([]byte, 0, len(src)/2))
}

// Decompress decompresses zstd data under the absolute MaxBlockBytes ceiling.
// Callers that know how many values the block holds should use
// DecompressBounded with DecodeCeiling instead.
func Decompress(src []byte) ([]byte, error) {
	return DecompressBounded(src, MaxBlockBytes)
}

// DecompressBounded decompresses zstd data, refusing to expand it past
// maxDecoded bytes. The declared frame content size is checked first (cheap,
// and it stops the allocation before it happens); frames that don't declare one
// are still hard-bounded by the decoder's own memory ceiling.
func DecompressBounded(src []byte, maxDecoded int) ([]byte, error) {
	if len(src) == 0 {
		return nil, nil
	}
	if maxDecoded <= 0 || maxDecoded > MaxBlockBytes {
		maxDecoded = MaxBlockBytes
	}
	var h zstd.Header
	if err := h.Decode(src); err == nil && h.HasFCS && h.FrameContentSize > uint64(maxDecoded) {
		return nil, fmt.Errorf("zstd: declared decoded size %d exceeds ceiling %d", h.FrameContentSize, maxDecoded)
	}
	out, err := boundedDecoder(maxDecoded).DecodeAll(src, nil)
	if err != nil {
		return nil, fmt.Errorf("zstd decode (ceiling %d): %w", maxDecoded, err)
	}
	return out, nil
}
