package wal

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	enc "github.com/adham90/opentrace/internal/logstore/encoding"
)

// Flag bits indicating which optional fields are present.
const (
	FlagEnv            uint32 = 1 << 0
	FlagVersion        uint32 = 1 << 1
	FlagEventType      uint32 = 1 << 2
	FlagTraceID        uint32 = 1 << 3
	FlagSpanID         uint32 = 1 << 4
	FlagParentSpanID   uint32 = 1 << 5
	FlagRequestID      uint32 = 1 << 6
	FlagUserID         uint32 = 1 << 7
	FlagTenantID       uint32 = 1 << 8
	FlagSessionID      uint32 = 1 << 9
	FlagMethod         uint32 = 1 << 10
	FlagPath           uint32 = 1 << 11
	FlagStatus         uint32 = 1 << 12
	FlagDurationMs     uint32 = 1 << 13
	FlagController     uint32 = 1 << 14
	FlagAction         uint32 = 1 << 15
	FlagDbMs           uint32 = 1 << 16
	FlagDbCount        uint32 = 1 << 17
	FlagNPlusOne       uint32 = 1 << 18
	FlagSlowQueries    uint32 = 1 << 19
	FlagDupQueries     uint32 = 1 << 20
	FlagExceptionClass uint32 = 1 << 21
	FlagSourceFile     uint32 = 1 << 22
	FlagSourceLine     uint32 = 1 << 23
	FlagErrFingerprint uint32 = 1 << 24
	FlagBody           uint32 = 1 << 25
)

// Level enum values (1 byte in binary format).
var levelToEnum = map[string]byte{
	"trace": 0, "debug": 1, "info": 2, "warn": 3, "warning": 3, "error": 4, "fatal": 5,
}

var enumToLevel = []string{"trace", "debug", "info", "warn", "error", "fatal"}

// MarshalEntry serializes a chunk.Entry to compact binary format.
// Format: [4 bytes total_len] [8 bytes id] [8 bytes ts] [8 bytes received_at]
//
//	[1 byte level_enum] [4 bytes flags] [2+N service] [2+N message]
//	[optional fields based on flags]
func MarshalEntry(e *chunk.Entry) []byte {
	// Pre-allocate estimate
	buf := make([]byte, 0, 128)

	// Placeholder for total length (filled at end)
	buf = append(buf, 0, 0, 0, 0)

	// Fixed fields
	buf = binary.LittleEndian.AppendUint64(buf, uint64(e.ID))
	buf = binary.LittleEndian.AppendUint64(buf, uint64(e.Ts))
	buf = binary.LittleEndian.AppendUint64(buf, uint64(e.ReceivedAt))

	// Level as 1-byte enum
	lvl, ok := levelToEnum[e.Level]
	if !ok {
		lvl = 2 // default to "info"
	}
	buf = append(buf, lvl)

	// Compute flags
	var flags uint32
	if e.Env != "" {
		flags |= FlagEnv
	}
	if e.Version != "" {
		flags |= FlagVersion
	}
	if e.EventType != "" {
		flags |= FlagEventType
	}
	if e.TraceID != "" {
		flags |= FlagTraceID
	}
	if e.SpanID != "" {
		flags |= FlagSpanID
	}
	if e.ParentSpanID != "" {
		flags |= FlagParentSpanID
	}
	if e.RequestID != "" {
		flags |= FlagRequestID
	}
	if e.UserID != "" {
		flags |= FlagUserID
	}
	if e.TenantID != "" {
		flags |= FlagTenantID
	}
	if e.SessionID != "" {
		flags |= FlagSessionID
	}
	if e.Method != "" {
		flags |= FlagMethod
	}
	if e.Path != "" {
		flags |= FlagPath
	}
	if e.Status > 0 {
		flags |= FlagStatus
	}
	if e.DurationMs > 0 {
		flags |= FlagDurationMs
	}
	if e.Controller != "" {
		flags |= FlagController
	}
	if e.Action != "" {
		flags |= FlagAction
	}
	if e.DbMs > 0 {
		flags |= FlagDbMs
	}
	if e.DbCount > 0 {
		flags |= FlagDbCount
	}
	if e.NPlusOne != nil {
		flags |= FlagNPlusOne
	}
	if e.SlowQueries > 0 {
		flags |= FlagSlowQueries
	}
	if e.DupQueries > 0 {
		flags |= FlagDupQueries
	}
	if e.ExceptionClass != "" {
		flags |= FlagExceptionClass
	}
	if e.SourceFile != "" {
		flags |= FlagSourceFile
	}
	if e.SourceLine > 0 {
		flags |= FlagSourceLine
	}
	if e.ErrorFingerprint != "" {
		flags |= FlagErrFingerprint
	}
	if len(e.Body) > 0 {
		flags |= FlagBody
	}
	buf = binary.LittleEndian.AppendUint32(buf, flags)

	// Required string fields
	buf = appendString(buf, e.Service)
	buf = appendString(buf, e.Message)

	// Optional fields (in flag order)
	if flags&FlagEnv != 0 {
		buf = appendString(buf, e.Env)
	}
	if flags&FlagVersion != 0 {
		buf = appendString(buf, e.Version)
	}
	if flags&FlagEventType != 0 {
		buf = appendString(buf, e.EventType)
	}
	if flags&FlagTraceID != 0 {
		buf = appendString(buf, e.TraceID)
	}
	if flags&FlagSpanID != 0 {
		buf = appendString(buf, e.SpanID)
	}
	if flags&FlagParentSpanID != 0 {
		buf = appendString(buf, e.ParentSpanID)
	}
	if flags&FlagRequestID != 0 {
		buf = appendString(buf, e.RequestID)
	}
	if flags&FlagUserID != 0 {
		buf = appendString(buf, e.UserID)
	}
	if flags&FlagTenantID != 0 {
		buf = appendString(buf, e.TenantID)
	}
	if flags&FlagSessionID != 0 {
		buf = appendString(buf, e.SessionID)
	}
	if flags&FlagMethod != 0 {
		buf = appendString(buf, e.Method)
	}
	if flags&FlagPath != 0 {
		buf = appendString(buf, e.Path)
	}
	if flags&FlagStatus != 0 {
		buf = binary.LittleEndian.AppendUint16(buf, uint16(e.Status))
	}
	if flags&FlagDurationMs != 0 {
		buf = enc.PutUvarint(buf, uint64(e.DurationMs))
	}
	if flags&FlagController != 0 {
		buf = appendString(buf, e.Controller)
	}
	if flags&FlagAction != 0 {
		buf = appendString(buf, e.Action)
	}
	if flags&FlagDbMs != 0 {
		buf = enc.PutUvarint(buf, uint64(e.DbMs))
	}
	if flags&FlagDbCount != 0 {
		buf = enc.PutUvarint(buf, uint64(e.DbCount))
	}
	if flags&FlagNPlusOne != 0 {
		if *e.NPlusOne {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
	}
	if flags&FlagSlowQueries != 0 {
		buf = enc.PutUvarint(buf, uint64(e.SlowQueries))
	}
	if flags&FlagDupQueries != 0 {
		buf = enc.PutUvarint(buf, uint64(e.DupQueries))
	}
	if flags&FlagExceptionClass != 0 {
		buf = appendString(buf, e.ExceptionClass)
	}
	if flags&FlagSourceFile != 0 {
		buf = appendString(buf, e.SourceFile)
	}
	if flags&FlagSourceLine != 0 {
		buf = enc.PutUvarint(buf, uint64(e.SourceLine))
	}
	if flags&FlagErrFingerprint != 0 {
		buf = appendString(buf, e.ErrorFingerprint)
	}
	if flags&FlagBody != 0 {
		// Body stored as zstd-compressed JSON
		compressed := enc.Compress(e.Body)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(compressed)))
		buf = append(buf, compressed...)
	}

	// Write total length at the beginning (excludes the 4-byte length prefix itself)
	binary.LittleEndian.PutUint32(buf[:4], uint32(len(buf)-4))
	return buf
}

// UnmarshalEntry deserializes a chunk.Entry from compact binary format.
func UnmarshalEntry(data []byte) (*chunk.Entry, error) {
	if len(data) < 29 { // minimum: id(8) + ts(8) + received_at(8) + level(1) + flags(4)
		return nil, fmt.Errorf("entry data too short: %d bytes", len(data))
	}

	e := &chunk.Entry{}
	off := 0

	e.ID = int64(binary.LittleEndian.Uint64(data[off:]))
	off += 8
	e.Ts = int64(binary.LittleEndian.Uint64(data[off:]))
	off += 8
	e.ReceivedAt = int64(binary.LittleEndian.Uint64(data[off:]))
	off += 8

	lvlEnum := data[off]
	off++
	if int(lvlEnum) < len(enumToLevel) {
		e.Level = enumToLevel[lvlEnum]
	} else {
		e.Level = "info"
	}

	flags := binary.LittleEndian.Uint32(data[off:])
	off += 4

	// Required strings
	var err error
	e.Service, off, err = readString(data, off)
	if err != nil {
		return nil, fmt.Errorf("read service: %w", err)
	}
	e.Message, off, err = readString(data, off)
	if err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}

	// Optional fields
	if flags&FlagEnv != 0 {
		e.Env, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read env: %w", err)
		}
	}
	if flags&FlagVersion != 0 {
		e.Version, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read version: %w", err)
		}
	}
	if flags&FlagEventType != 0 {
		e.EventType, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read event_type: %w", err)
		}
	}
	if flags&FlagTraceID != 0 {
		e.TraceID, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read trace_id: %w", err)
		}
	}
	if flags&FlagSpanID != 0 {
		e.SpanID, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read span_id: %w", err)
		}
	}
	if flags&FlagParentSpanID != 0 {
		e.ParentSpanID, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read parent_span_id: %w", err)
		}
	}
	if flags&FlagRequestID != 0 {
		e.RequestID, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read request_id: %w", err)
		}
	}
	if flags&FlagUserID != 0 {
		e.UserID, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read user_id: %w", err)
		}
	}
	if flags&FlagTenantID != 0 {
		e.TenantID, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read tenant_id: %w", err)
		}
	}
	if flags&FlagSessionID != 0 {
		e.SessionID, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read session_id: %w", err)
		}
	}
	if flags&FlagMethod != 0 {
		e.Method, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read method: %w", err)
		}
	}
	if flags&FlagPath != 0 {
		e.Path, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read path: %w", err)
		}
	}
	if flags&FlagStatus != 0 {
		if off+2 > len(data) {
			return nil, fmt.Errorf("read status: truncated")
		}
		e.Status = int(binary.LittleEndian.Uint16(data[off:]))
		off += 2
	}
	if flags&FlagDurationMs != 0 {
		v, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, fmt.Errorf("read duration_ms: invalid uvarint")
		}
		e.DurationMs = int(v)
		off += n
	}
	if flags&FlagController != 0 {
		e.Controller, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read controller: %w", err)
		}
	}
	if flags&FlagAction != 0 {
		e.Action, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read action: %w", err)
		}
	}
	if flags&FlagDbMs != 0 {
		v, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, fmt.Errorf("read db_ms: invalid uvarint")
		}
		e.DbMs = int(v)
		off += n
	}
	if flags&FlagDbCount != 0 {
		v, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, fmt.Errorf("read db_count: invalid uvarint")
		}
		e.DbCount = int(v)
		off += n
	}
	if flags&FlagNPlusOne != 0 {
		if off >= len(data) {
			return nil, fmt.Errorf("read n_plus_one: truncated")
		}
		v := data[off] != 0
		e.NPlusOne = &v
		off++
	}
	if flags&FlagSlowQueries != 0 {
		v, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, fmt.Errorf("read slow_queries: invalid uvarint")
		}
		e.SlowQueries = int(v)
		off += n
	}
	if flags&FlagDupQueries != 0 {
		v, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, fmt.Errorf("read dup_queries: invalid uvarint")
		}
		e.DupQueries = int(v)
		off += n
	}
	if flags&FlagExceptionClass != 0 {
		e.ExceptionClass, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read exception_class: %w", err)
		}
	}
	if flags&FlagSourceFile != 0 {
		e.SourceFile, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read source_file: %w", err)
		}
	}
	if flags&FlagSourceLine != 0 {
		v, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, fmt.Errorf("read source_line: invalid uvarint")
		}
		e.SourceLine = int(v)
		off += n
	}
	if flags&FlagErrFingerprint != 0 {
		e.ErrorFingerprint, off, err = readString(data, off)
		if err != nil {
			return nil, fmt.Errorf("read error_fingerprint: %w", err)
		}
	}
	if flags&FlagBody != 0 {
		if off+4 > len(data) {
			return nil, fmt.Errorf("read body length: truncated")
		}
		bodyLen := int(binary.LittleEndian.Uint32(data[off:]))
		off += 4
		if off+bodyLen > len(data) {
			return nil, fmt.Errorf("read body: truncated (need %d, have %d)", bodyLen, len(data)-off)
		}
		decompressed, err := enc.Decompress(data[off : off+bodyLen])
		if err != nil {
			return nil, fmt.Errorf("decompress body: %w", err)
		}
		e.Body = decompressed
		off += bodyLen
	}

	_ = off // suppress unused variable warning
	return e, nil
}

// ReadEntries reads all entries from a WAL file (reader positioned at start).
func ReadEntries(r io.Reader) ([]chunk.Entry, error) {
	var entries []chunk.Entry
	lenBuf := make([]byte, 4)

	for {
		_, err := io.ReadFull(r, lenBuf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return entries, fmt.Errorf("read entry length: %w", err)
		}

		entryLen := int(binary.LittleEndian.Uint32(lenBuf))
		if entryLen <= 0 || entryLen > 10<<20 { // 10MB max entry
			return entries, fmt.Errorf("invalid entry length: %d", entryLen)
		}

		entryData := make([]byte, entryLen)
		_, err = io.ReadFull(r, entryData)
		if err != nil {
			return entries, fmt.Errorf("read entry data: %w", err)
		}

		entry, err := UnmarshalEntry(entryData)
		if err != nil {
			return entries, fmt.Errorf("unmarshal entry: %w", err)
		}
		entries = append(entries, *entry)
	}

	return entries, nil
}

// --- helpers ---

func appendString(buf []byte, s string) []byte {
	buf = binary.LittleEndian.AppendUint16(buf, uint16(len(s)))
	return append(buf, s...)
}

func readString(data []byte, off int) (string, int, error) {
	if off+2 > len(data) {
		return "", off, fmt.Errorf("truncated string length at offset %d", off)
	}
	sLen := int(binary.LittleEndian.Uint16(data[off:]))
	off += 2
	if off+sLen > len(data) {
		return "", off, fmt.Errorf("truncated string data at offset %d (need %d)", off, sLen)
	}
	s := string(data[off : off+sLen])
	return s, off + sLen, nil
}
