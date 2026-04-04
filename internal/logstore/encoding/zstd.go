package encoding

import (
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Shared zstd encoder/decoder pool — allocating these is expensive.
var (
	encoderOnce sync.Once
	decoderOnce sync.Once
	sharedEnc   *zstd.Encoder
	sharedDec   *zstd.Decoder
)

func zstdEncoder() *zstd.Encoder {
	encoderOnce.Do(func() {
		var err error
		sharedEnc, err = zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstd.SpeedDefault),
			zstd.WithEncoderConcurrency(1), // single goroutine, save memory
		)
		if err != nil {
			panic("zstd encoder init: " + err.Error())
		}
	})
	return sharedEnc
}

func zstdDecoder() *zstd.Decoder {
	decoderOnce.Do(func() {
		var err error
		sharedDec, err = zstd.NewReader(nil,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxMemory(64<<20), // 64MB max decompression
		)
		if err != nil {
			panic("zstd decoder init: " + err.Error())
		}
	})
	return sharedDec
}

// Compress compresses data using zstd.
func Compress(src []byte) []byte {
	return zstdEncoder().EncodeAll(src, make([]byte, 0, len(src)/2))
}

// Decompress decompresses zstd data.
func Decompress(src []byte) ([]byte, error) {
	return zstdDecoder().DecodeAll(src, nil)
}
