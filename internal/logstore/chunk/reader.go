package chunk

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"

	enc "github.com/adham90/opentrace/internal/logstore/encoding"
)

// ErrColumnMissing reports that a column is absent from the chunk directory.
// This is the only readColumnRaw failure the Read* helpers are allowed to paper
// over with default values — it is expected under schema evolution. Every other
// failure (corrupt directory entry, I/O error) must reach the caller.
var ErrColumnMissing = errors.New("column not found in chunk")

// Reader reads columns from a sealed chunk file.
type Reader struct {
	f          *os.File
	EntryCount int
	Version    int
	lenFormat  enc.LenFormat
	directory  map[string]DirectoryEntry
}

// OpenReader opens a chunk file for reading.
func OpenReader(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open chunk: %w", err)
	}

	// Read header
	header := make([]byte, HeaderSize)
	if _, err := f.ReadAt(header, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("read header: %w", err)
	}

	if string(header[0:4]) != Magic {
		f.Close()
		return nil, fmt.Errorf("invalid magic: %q", header[0:4])
	}

	version := int(binary.LittleEndian.Uint16(header[4:]))
	if version < MinReadableVersion || version > Version {
		f.Close()
		return nil, fmt.Errorf("unsupported version: %d", version)
	}

	entryCount := int(binary.LittleEndian.Uint32(header[8:]))
	colCount := int(binary.LittleEndian.Uint16(header[12:]))
	dirOffset := binary.LittleEndian.Uint64(header[16:])

	// Read directory
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat chunk: %w", err)
	}
	// Validate header fields before using them for allocation/reads. A corrupt
	// or truncated chunk (bad disk, partial write) could otherwise yield a
	// negative or multi-GB length and panic the reader — which, running inside
	// an unrecovered search goroutine, would crash the whole server.
	if int64(dirOffset) < HeaderSize || int64(dirOffset) > fi.Size() {
		f.Close()
		return nil, fmt.Errorf("corrupt chunk: directory offset %d out of range (file size %d)", dirOffset, fi.Size())
	}
	if entryCount < 0 || entryCount > maxChunkEntries {
		f.Close()
		return nil, fmt.Errorf("corrupt chunk: entry count %d out of range", entryCount)
	}
	if colCount < 0 || colCount > maxChunkColumns {
		f.Close()
		return nil, fmt.Errorf("corrupt chunk: column count %d out of range", colCount)
	}
	dirSize := fi.Size() - int64(dirOffset)
	dirBuf := make([]byte, dirSize)
	if _, err := f.ReadAt(dirBuf, int64(dirOffset)); err != nil {
		f.Close()
		return nil, fmt.Errorf("read directory: %w", err)
	}

	directory, err := unmarshalDirectory(dirBuf, colCount)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("parse directory: %w", err)
	}

	dirMap := make(map[string]DirectoryEntry, len(directory))
	for _, e := range directory {
		dirMap[e.Name] = e
	}

	return &Reader{
		f:          f,
		EntryCount: entryCount,
		Version:    version,
		lenFormat:  readerLenFormat(version),
		directory:  dirMap,
	}, nil
}

// readerLenFormat maps a chunk format version to its string length framing.
// v1 files were written with a bare uint16 prefix; v2 added the extended
// framing that can represent values over 65535 bytes.
func readerLenFormat(version int) enc.LenFormat {
	if version < 2 {
		return enc.LenUint16
	}
	return enc.LenExtended
}

// Close closes the chunk file.
func (r *Reader) Close() error {
	return r.f.Close()
}

// HasColumn returns true if the column exists in this chunk.
func (r *Reader) HasColumn(name string) bool {
	_, ok := r.directory[name]
	return ok
}

// NullCount returns the number of null values in a column.
func (r *Reader) NullCount(name string) int {
	if e, ok := r.directory[name]; ok {
		return int(e.NullCount)
	}
	return r.EntryCount // missing column = all null
}

// readColumnRaw reads the raw compressed bytes for a column.
func (r *Reader) readColumnRaw(name string) ([]byte, ColumnType, error) {
	entry, ok := r.directory[name]
	if !ok {
		return nil, 0, fmt.Errorf("%w: %q", ErrColumnMissing, name)
	}

	// Guard against a corrupt directory entry claiming an absurd size.
	if fi, err := r.f.Stat(); err == nil && int64(entry.CompressedSize) > fi.Size() {
		return nil, 0, fmt.Errorf("corrupt chunk: column %q size %d exceeds file size %d", name, entry.CompressedSize, fi.Size())
	}
	data := make([]byte, entry.CompressedSize)
	if _, err := r.f.ReadAt(data, int64(entry.Offset)); err != nil {
		return nil, 0, fmt.Errorf("read column %s at offset %d: %w", name, entry.Offset, err)
	}
	return data, entry.Type, nil
}

// missingColumn reports whether err means "this chunk predates the column".
// Only that case may be answered with default values; corruption and I/O errors
// must propagate so callers see a failure instead of fabricated data.
func missingColumn(err error) bool {
	return errors.Is(err, ErrColumnMissing)
}

// ReadDeltaInt64 reads a delta+varint encoded int64 column.
func (r *Reader) ReadDeltaInt64(name string) ([]int64, error) {
	data, _, err := r.readColumnRaw(name)
	if err != nil {
		if missingColumn(err) {
			return make([]int64, r.EntryCount), nil // schema evolution: all zeros
		}
		return nil, err
	}
	return enc.DeltaDecode(data, r.EntryCount)
}

// ReadZstdInt64 reads a zstd block encoded int64 column.
func (r *Reader) ReadZstdInt64(name string) ([]int64, error) {
	data, _, err := r.readColumnRaw(name)
	if err != nil {
		if missingColumn(err) {
			return make([]int64, r.EntryCount), nil
		}
		return nil, err
	}
	return enc.ZstdBlockDecodeInt64(data, r.EntryCount)
}

// ReadDictBitpack reads a dictionary+bitpack encoded string column.
// Format: [4 bytes dict_len] [dict: 2-byte count + entries] [bitpack data]
func (r *Reader) ReadDictBitpack(name string) ([]string, error) {
	data, _, err := r.readColumnRaw(name)
	if err != nil {
		if missingColumn(err) {
			return make([]string, r.EntryCount), nil
		}
		return nil, err
	}

	if len(data) < 4 {
		return nil, fmt.Errorf("dict+bitpack data too short")
	}

	dictLen := int(binary.LittleEndian.Uint32(data[:4]))
	if 4+dictLen > len(data) {
		return nil, fmt.Errorf("dict+bitpack: dict overflows data")
	}

	dictData := data[4 : 4+dictLen]
	bitpackData := data[4+dictLen:]

	// Parse dictionary: [2 bytes count] [length-prefixed entries]
	dict, err := deserializeDictOnly(dictData, r.lenFormat)
	if err != nil {
		return nil, fmt.Errorf("dict parse: %w", err)
	}

	// Decode bitpack indices
	indices, err := enc.BitpackDecode(bitpackData)
	if err != nil {
		return nil, fmt.Errorf("bitpack decode: %w", err)
	}

	// Resolve indices through dictionary
	result := make([]string, len(indices))
	for i, idx := range indices {
		if int(idx) < len(dict) {
			result[i] = dict[idx]
		}
	}
	return result, nil
}

// deserializeDictOnly reads [2 bytes count][length-prefixed entries].
func deserializeDictOnly(data []byte, f enc.LenFormat) ([]string, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("dict too short")
	}
	count := int(binary.LittleEndian.Uint16(data[:2]))
	offset := 2
	dict := make([]string, 0, count)
	for range count {
		var s string
		var err error
		s, offset, err = f.ReadString(data, offset)
		if err != nil {
			return nil, fmt.Errorf("dict entry %d: %w", len(dict), err)
		}
		dict = append(dict, s)
	}
	return dict, nil
}

// ReadDictZstd reads a dictionary+zstd encoded string column.
func (r *Reader) ReadDictZstd(name string) ([]string, error) {
	data, _, err := r.readColumnRaw(name)
	if err != nil {
		if missingColumn(err) {
			return make([]string, r.EntryCount), nil
		}
		return nil, err
	}
	d, err := enc.DictUnmarshal(data, r.EntryCount, r.lenFormat)
	if err != nil {
		return nil, fmt.Errorf("dict unmarshal %s: %w", name, err)
	}
	return enc.DictLookup(d), nil
}

// ReadZstdBlockStrings reads a zstd block encoded string column.
func (r *Reader) ReadZstdBlockStrings(name string) ([]string, error) {
	data, _, err := r.readColumnRaw(name)
	if err != nil {
		if missingColumn(err) {
			return make([]string, r.EntryCount), nil
		}
		return nil, err
	}
	return enc.ZstdBlockDecodeStrings(data, r.EntryCount, r.lenFormat)
}

// ReadSparseStrings reads a sparse+zstd encoded string column.
func (r *Reader) ReadSparseStrings(name string) ([]string, error) {
	data, _, err := r.readColumnRaw(name)
	if err != nil {
		if missingColumn(err) {
			return make([]string, r.EntryCount), nil
		}
		return nil, err
	}
	return enc.UnsparseStrings(data, r.EntryCount, r.lenFormat)
}

// ReadSparseInt64 reads a sparse+varint+zstd encoded int64 column.
func (r *Reader) ReadSparseInt64(name string) ([]*int64, error) {
	data, _, err := r.readColumnRaw(name)
	if err != nil {
		if missingColumn(err) {
			return make([]*int64, r.EntryCount), nil
		}
		return nil, err
	}
	return enc.UnsparseInt64(data, r.EntryCount)
}

// ReadSparseBool reads a sparse bool column.
func (r *Reader) ReadSparseBool(name string) ([]*bool, error) {
	data, _, err := r.readColumnRaw(name)
	if err != nil {
		if missingColumn(err) {
			return make([]*bool, r.EntryCount), nil
		}
		return nil, err
	}
	return enc.UnsparseBool(data, r.EntryCount)
}

// ReadSparseBytes reads a sparse bytes column (body).
func (r *Reader) ReadSparseBytes(name string) ([][]byte, error) {
	data, _, err := r.readColumnRaw(name)
	if err != nil {
		if missingColumn(err) {
			return make([][]byte, r.EntryCount), nil
		}
		return nil, err
	}
	return enc.UnsparseBytes(data, r.EntryCount)
}

// ReadColumn reads any column by name, dispatching to the correct decoder.
// Returns a generic interface — caller must type-assert based on column type.
func (r *Reader) ReadColumn(name string) (any, error) {
	entry, ok := r.directory[name]
	if !ok {
		return nil, nil // schema evolution: column doesn't exist
	}

	switch entry.Type {
	case ColDeltaVarint:
		return r.ReadDeltaInt64(name)
	case ColZstdInt64:
		return r.ReadZstdInt64(name)
	case ColDictBitpack:
		return r.ReadDictBitpack(name)
	case ColDictZstd:
		return r.ReadDictZstd(name)
	case ColZstdBlock:
		return r.ReadZstdBlockStrings(name)
	case ColSparseString, ColSparseDictZstd:
		return r.ReadSparseStrings(name)
	case ColSparseInt64:
		return r.ReadSparseInt64(name)
	case ColSparseBool:
		return r.ReadSparseBool(name)
	case ColSparseBytes:
		return r.ReadSparseBytes(name)
	default:
		return nil, fmt.Errorf("unknown column type %d for %s", entry.Type, name)
	}
}

// unmarshalDirectory parses the column directory from the footer.
func unmarshalDirectory(data []byte, expectedCount int) ([]DirectoryEntry, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("directory too short")
	}

	count := int(binary.LittleEndian.Uint16(data[:2]))
	if count != expectedCount {
		return nil, fmt.Errorf("directory count mismatch: header says %d, directory says %d", expectedCount, count)
	}

	entries := make([]DirectoryEntry, 0, count)
	offset := 2
	for range count {
		if offset >= len(data) {
			return nil, fmt.Errorf("directory truncated at entry %d", len(entries))
		}

		nameLen := int(data[offset])
		offset++
		if offset+nameLen > len(data) {
			return nil, fmt.Errorf("directory name truncated")
		}
		name := string(data[offset : offset+nameLen])
		offset += nameLen

		if offset+17 > len(data) {
			return nil, fmt.Errorf("directory entry %q truncated", name)
		}

		colType := ColumnType(data[offset])
		offset++
		colOffset := binary.LittleEndian.Uint64(data[offset:])
		offset += 8
		compressedSize := binary.LittleEndian.Uint32(data[offset:])
		offset += 4
		nullCount := binary.LittleEndian.Uint32(data[offset:])
		offset += 4

		entries = append(entries, DirectoryEntry{
			Name:           name,
			Type:           colType,
			Offset:         colOffset,
			CompressedSize: compressedSize,
			NullCount:      nullCount,
		})
	}

	return entries, nil
}
