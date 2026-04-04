package chunk

import (
	"encoding/binary"
	"fmt"
	"os"

	enc "github.com/adham90/opentrace/internal/logstore/encoding"
)

// File format constants.
const (
	Magic       = "OTCL"
	Version     = 1
	HeaderSize  = 64
)

// DirectoryEntry describes one column in the chunk footer.
type DirectoryEntry struct {
	Name           string
	Type           ColumnType
	Offset         uint64
	CompressedSize uint32
	RawSize        uint32
	NullCount      uint32
}

// Writer writes a columnar chunk file.
type Writer struct {
	f          *os.File
	entries    []Entry
	offset     uint64
	directory  []DirectoryEntry
}

// NewWriter creates a chunk writer that will write to the given path.
// Entries must be provided upfront (collected during seal pass 1).
func NewWriter(path string, entries []Entry) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create chunk: %w", err)
	}

	w := &Writer{
		f:       f,
		entries: entries,
		offset:  HeaderSize, // skip header, write it at the end
	}

	// Write placeholder header (will be overwritten at close)
	if _, err := f.Write(make([]byte, HeaderSize)); err != nil {
		f.Close()
		return nil, fmt.Errorf("write header placeholder: %w", err)
	}

	return w, nil
}

// WriteAllColumns encodes and writes all 32 columns.
func (w *Writer) WriteAllColumns() error {
	for _, col := range Schema {
		data, nullCount, err := w.encodeColumn(col)
		if err != nil {
			return fmt.Errorf("encode column %s: %w", col.Name, err)
		}

		n, err := w.f.Write(data)
		if err != nil {
			return fmt.Errorf("write column %s: %w", col.Name, err)
		}

		w.directory = append(w.directory, DirectoryEntry{
			Name:           col.Name,
			Type:           col.Type,
			Offset:         w.offset,
			CompressedSize: uint32(n),
			NullCount:      uint32(nullCount),
		})
		w.offset += uint64(n)
	}
	return nil
}

// Close writes the directory footer and header, then closes the file.
func (w *Writer) Close() error {
	// Write directory at current offset
	dirOffset := w.offset
	dirData := marshalDirectory(w.directory)
	if _, err := w.f.Write(dirData); err != nil {
		w.f.Close()
		return fmt.Errorf("write directory: %w", err)
	}

	// Write header at beginning of file
	header := make([]byte, HeaderSize)
	copy(header[0:4], Magic)
	binary.LittleEndian.PutUint16(header[4:], Version)
	binary.LittleEndian.PutUint32(header[8:], uint32(len(w.entries)))
	binary.LittleEndian.PutUint16(header[12:], uint16(len(w.directory)))
	binary.LittleEndian.PutUint64(header[16:], dirOffset)

	if _, err := w.f.WriteAt(header, 0); err != nil {
		w.f.Close()
		return fmt.Errorf("write header: %w", err)
	}

	return w.f.Close()
}

// encodeColumn encodes a single column from the entry slice.
// Returns encoded bytes and null count.
func (w *Writer) encodeColumn(col ColumnDef) ([]byte, int, error) {
	n := len(w.entries)

	switch col.Type {
	case ColDeltaVarint:
		values := w.extractInt64(col.Name)
		return enc.DeltaEncode(values), 0, nil

	case ColZstdInt64:
		values := w.extractInt64(col.Name)
		return enc.ZstdBlockEncodeInt64(values), 0, nil

	case ColDictBitpack:
		values := w.extractString(col.Name)
		d := enc.DictEncode(values)
		bits := enc.BitsNeeded(len(d.Dict))
		indices8 := make([]uint8, len(d.Indices))
		for i, idx := range d.Indices {
			indices8[i] = uint8(idx)
		}
		// Serialize dictionary manually (not DictMarshal — different format)
		dictBuf := serializeDictOnly(d.Dict)
		bitpackBytes := enc.BitpackEncode(indices8, bits)
		// Combine: [4 bytes dict_len] [dict] [bitpack]
		result := binary.LittleEndian.AppendUint32(nil, uint32(len(dictBuf)))
		result = append(result, dictBuf...)
		result = append(result, bitpackBytes...)
		return result, 0, nil

	case ColDictZstd:
		values := w.extractString(col.Name)
		d := enc.DictEncode(values)
		return enc.DictMarshal(d), 0, nil

	case ColZstdBlock:
		values := w.extractString(col.Name)
		return enc.ZstdBlockEncodeStrings(values), 0, nil

	case ColSparseString:
		values := w.extractString(col.Name)
		nullCount := 0
		for _, v := range values {
			if v == "" {
				nullCount++
			}
		}
		return enc.SparseStrings(values), nullCount, nil

	case ColSparseInt64:
		values := w.extractOptionalInt64(col.Name)
		nullCount := 0
		for _, v := range values {
			if v == nil {
				nullCount++
			}
		}
		return enc.SparseInt64(values), nullCount, nil

	case ColSparseBool:
		values := w.extractOptionalBool(col.Name)
		nullCount := 0
		for _, v := range values {
			if v == nil {
				nullCount++
			}
		}
		return enc.SparseBool(values), nullCount, nil

	case ColSparseBytes:
		values := make([][]byte, n)
		for i, e := range w.entries {
			if len(e.Body) > 0 {
				values[i] = []byte(e.Body)
			}
		}
		nullCount := 0
		for _, v := range values {
			if v == nil {
				nullCount++
			}
		}
		return enc.SparseBytes(values), nullCount, nil

	case ColSparseDictZstd:
		values := w.extractString(col.Name)
		// Sparse dict: treat empty strings as null, dictionary-encode non-null values
		// Use SparseStrings for simplicity (dict optimization can be added later)
		nullCount := 0
		for _, v := range values {
			if v == "" {
				nullCount++
			}
		}
		return enc.SparseStrings(values), nullCount, nil

	default:
		return nil, 0, fmt.Errorf("unknown column type: %d", col.Type)
	}
}

// extractInt64 extracts a non-nullable int64 column.
func (w *Writer) extractInt64(name string) []int64 {
	values := make([]int64, len(w.entries))
	for i, e := range w.entries {
		switch name {
		case "id":
			values[i] = e.ID
		case "ts":
			values[i] = e.Ts
		case "received_at":
			values[i] = e.ReceivedAt
		}
	}
	return values
}

// extractString extracts a string column (empty string = null for sparse).
func (w *Writer) extractString(name string) []string {
	values := make([]string, len(w.entries))
	for i, e := range w.entries {
		switch name {
		case "level":
			values[i] = e.Level
		case "service":
			values[i] = e.Service
		case "env":
			values[i] = e.Env
		case "version":
			values[i] = e.Version
		case "message":
			values[i] = e.Message
		case "event_type":
			values[i] = e.EventType
		case "trace_id":
			values[i] = e.TraceID
		case "span_id":
			values[i] = e.SpanID
		case "parent_span_id":
			values[i] = e.ParentSpanID
		case "request_id":
			values[i] = e.RequestID
		case "user_id":
			values[i] = e.UserID
		case "tenant_id":
			values[i] = e.TenantID
		case "session_id":
			values[i] = e.SessionID
		case "method":
			values[i] = e.Method
		case "path":
			values[i] = e.Path
		case "status":
			if e.Status > 0 {
				values[i] = fmt.Sprintf("%d", e.Status)
			}
		case "handler":
			values[i] = e.Handler
		case "error_class":
			values[i] = e.ErrorClass
		case "source_file":
			values[i] = e.SourceFile
		case "error_fingerprint":
			values[i] = e.ErrorFingerprint
		}
	}
	return values
}

// extractOptionalInt64 extracts a nullable int64 column.
// Zero values are treated as null for sparse columns.
func (w *Writer) extractOptionalInt64(name string) []*int64 {
	values := make([]*int64, len(w.entries))
	for i, e := range w.entries {
		var v int64
		var present bool
		switch name {
		case "duration_ms":
			v, present = int64(e.DurationMs), e.DurationMs > 0
		case "db_ms":
			v, present = int64(e.DbMs), e.DbMs > 0
		case "db_count":
			v, present = int64(e.DbCount), e.DbCount > 0
		case "slow_queries":
			v, present = int64(e.SlowQueries), e.SlowQueries > 0
		case "dup_queries":
			v, present = int64(e.DupQueries), e.DupQueries > 0
		case "source_line":
			v, present = int64(e.SourceLine), e.SourceLine > 0
		}
		if present {
			val := v
			values[i] = &val
		}
	}
	return values
}

// extractOptionalBool extracts a nullable bool column.
func (w *Writer) extractOptionalBool(name string) []*bool {
	values := make([]*bool, len(w.entries))
	for i, e := range w.entries {
		switch name {
		case "n_plus_one":
			values[i] = e.NPlusOne
		}
	}
	return values
}

// serializeDictOnly writes a dictionary as [2 bytes count][entries: 2-byte len + string]
func serializeDictOnly(dict []string) []byte {
	buf := binary.LittleEndian.AppendUint16(nil, uint16(len(dict)))
	for _, s := range dict {
		buf = binary.LittleEndian.AppendUint16(buf, uint16(len(s)))
		buf = append(buf, s...)
	}
	return buf
}

// marshalDirectory serializes the column directory.
// Format: [2 bytes entry_count] [entries: 1-byte name_len + name + 1-byte type + 8-byte offset + 4-byte compressed_size + 4-byte null_count]
func marshalDirectory(entries []DirectoryEntry) []byte {
	buf := binary.LittleEndian.AppendUint16(nil, uint16(len(entries)))
	for _, e := range entries {
		buf = append(buf, byte(len(e.Name)))
		buf = append(buf, e.Name...)
		buf = append(buf, byte(e.Type))
		buf = binary.LittleEndian.AppendUint64(buf, e.Offset)
		buf = binary.LittleEndian.AppendUint32(buf, e.CompressedSize)
		buf = binary.LittleEndian.AppendUint32(buf, e.NullCount)
	}
	return buf
}
