package wal

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	enc "github.com/adham90/opentrace/internal/logstore/encoding"
)

// Flag bits indicating which optional fields are present (uint64 = 64 bits).
const (
	// Common context
	FlagEnv          uint64 = 1 << 0
	FlagVersion      uint64 = 1 << 1
	FlagHost         uint64 = 1 << 2
	FlagKind         uint64 = 1 << 3
	FlagEventType    uint64 = 1 << 4
	FlagTraceID      uint64 = 1 << 5
	FlagSpanID       uint64 = 1 << 6
	FlagParentSpanID uint64 = 1 << 7
	FlagRequestID    uint64 = 1 << 8
	FlagUserID       uint64 = 1 << 9
	FlagTenantID     uint64 = 1 << 10
	FlagSessionID    uint64 = 1 << 11

	// HTTP request
	FlagMethod     uint64 = 1 << 12
	FlagPath       uint64 = 1 << 13
	FlagRoute      uint64 = 1 << 14
	FlagHandler    uint64 = 1 << 15
	FlagStatus     uint64 = 1 << 16
	FlagDurationMs uint64 = 1 << 17

	// Performance
	FlagDbMs        uint64 = 1 << 18
	FlagDbCount     uint64 = 1 << 19
	FlagCacheMs     uint64 = 1 << 20
	FlagCacheHits   uint64 = 1 << 21
	FlagCacheMisses uint64 = 1 << 22
	FlagExtMs       uint64 = 1 << 23
	FlagExtCount    uint64 = 1 << 24
	FlagRenderMs    uint64 = 1 << 25
	FlagAllocCount  uint64 = 1 << 26
	FlagMemDeltaMb  uint64 = 1 << 27
	FlagNPlusOne    uint64 = 1 << 28
	FlagSlowQueries uint64 = 1 << 29
	FlagDupQueries  uint64 = 1 << 30

	// Error tracking
	FlagErrorClass      uint64 = 1 << 31
	FlagErrorMessage    uint64 = 1 << 32
	FlagSourceFile      uint64 = 1 << 33
	FlagSourceLine      uint64 = 1 << 34
	FlagErrFingerprint  uint64 = 1 << 35

	// Job
	FlagJobClass uint64 = 1 << 36
	FlagJobQueue uint64 = 1 << 37
	FlagJobID    uint64 = 1 << 38
	FlagQueueMs  uint64 = 1 << 39

	// Body
	FlagBody uint64 = 1 << 40
)

// Level enum values (1 byte in binary format).
var levelToEnum = map[string]byte{
	"trace": 0, "debug": 1, "info": 2, "warn": 3, "warning": 3, "error": 4, "fatal": 5,
}

var enumToLevel = []string{"trace", "debug", "info", "warn", "error", "fatal"}

// MarshalEntry serializes a chunk.Entry to compact binary format.
// Format: [4 bytes total_len] [8 bytes id] [8 bytes ts] [8 bytes received_at]
//
//	[1 byte level_enum] [8 bytes flags] [2+N service] [2+N message]
//	[optional fields based on flags]
func MarshalEntry(e *chunk.Entry) []byte {
	buf := make([]byte, 0, 256)

	// Placeholder for total length (filled at end)
	buf = append(buf, 0, 0, 0, 0)

	// Fixed fields
	buf = binary.LittleEndian.AppendUint64(buf, uint64(e.ID))
	buf = binary.LittleEndian.AppendUint64(buf, uint64(e.Ts))
	buf = binary.LittleEndian.AppendUint64(buf, uint64(e.ReceivedAt))

	lvl, ok := levelToEnum[e.Level]
	if !ok {
		lvl = 2
	}
	buf = append(buf, lvl)

	// Compute flags (uint64)
	var flags uint64
	setStr := func(f uint64, v string) { if v != "" { flags |= f } }
	setInt := func(f uint64, v int) { if v > 0 { flags |= f } }

	setStr(FlagEnv, e.Env)
	setStr(FlagVersion, e.Version)
	setStr(FlagHost, e.Host)
	setStr(FlagKind, e.Kind)
	setStr(FlagEventType, e.EventType)
	setStr(FlagTraceID, e.TraceID)
	setStr(FlagSpanID, e.SpanID)
	setStr(FlagParentSpanID, e.ParentSpanID)
	setStr(FlagRequestID, e.RequestID)
	setStr(FlagUserID, e.UserID)
	setStr(FlagTenantID, e.TenantID)
	setStr(FlagSessionID, e.SessionID)
	setStr(FlagMethod, e.Method)
	setStr(FlagPath, e.Path)
	setStr(FlagRoute, e.Route)
	setStr(FlagHandler, e.Handler)
	setInt(FlagStatus, e.Status)
	setInt(FlagDurationMs, e.DurationMs)
	setInt(FlagDbMs, e.DbMs)
	setInt(FlagDbCount, e.DbCount)
	setInt(FlagCacheMs, e.CacheMs)
	setInt(FlagCacheHits, e.CacheHits)
	setInt(FlagCacheMisses, e.CacheMisses)
	setInt(FlagExtMs, e.ExtMs)
	setInt(FlagExtCount, e.ExtCount)
	setInt(FlagRenderMs, e.RenderMs)
	setInt(FlagAllocCount, e.AllocCount)
	setInt(FlagMemDeltaMb, e.MemDeltaMb)
	if e.NPlusOne != nil { flags |= FlagNPlusOne }
	setInt(FlagSlowQueries, e.SlowQueries)
	setInt(FlagDupQueries, e.DupQueries)
	setStr(FlagErrorClass, e.ErrorClass)
	setStr(FlagErrorMessage, e.ErrorMessage)
	setStr(FlagSourceFile, e.SourceFile)
	setInt(FlagSourceLine, e.SourceLine)
	setStr(FlagErrFingerprint, e.ErrorFingerprint)
	setStr(FlagJobClass, e.JobClass)
	setStr(FlagJobQueue, e.JobQueue)
	setStr(FlagJobID, e.JobID)
	setInt(FlagQueueMs, e.QueueMs)
	if len(e.Body) > 0 { flags |= FlagBody }

	buf = binary.LittleEndian.AppendUint64(buf, flags)

	// Required string fields
	buf = appendString(buf, e.Service)
	buf = appendString(buf, e.Message)

	// Optional string fields
	writeStr := func(f uint64, v string) { if flags&f != 0 { buf = appendString(buf, v) } }
	writeUvar := func(f uint64, v int) { if flags&f != 0 { buf = enc.PutUvarint(buf, uint64(v)) } }

	writeStr(FlagEnv, e.Env)
	writeStr(FlagVersion, e.Version)
	writeStr(FlagHost, e.Host)
	writeStr(FlagKind, e.Kind)
	writeStr(FlagEventType, e.EventType)
	writeStr(FlagTraceID, e.TraceID)
	writeStr(FlagSpanID, e.SpanID)
	writeStr(FlagParentSpanID, e.ParentSpanID)
	writeStr(FlagRequestID, e.RequestID)
	writeStr(FlagUserID, e.UserID)
	writeStr(FlagTenantID, e.TenantID)
	writeStr(FlagSessionID, e.SessionID)
	writeStr(FlagMethod, e.Method)
	writeStr(FlagPath, e.Path)
	writeStr(FlagRoute, e.Route)
	writeStr(FlagHandler, e.Handler)
	if flags&FlagStatus != 0 {
		buf = binary.LittleEndian.AppendUint16(buf, uint16(e.Status))
	}
	writeUvar(FlagDurationMs, e.DurationMs)
	writeUvar(FlagDbMs, e.DbMs)
	writeUvar(FlagDbCount, e.DbCount)
	writeUvar(FlagCacheMs, e.CacheMs)
	writeUvar(FlagCacheHits, e.CacheHits)
	writeUvar(FlagCacheMisses, e.CacheMisses)
	writeUvar(FlagExtMs, e.ExtMs)
	writeUvar(FlagExtCount, e.ExtCount)
	writeUvar(FlagRenderMs, e.RenderMs)
	writeUvar(FlagAllocCount, e.AllocCount)
	writeUvar(FlagMemDeltaMb, e.MemDeltaMb)
	if flags&FlagNPlusOne != 0 {
		if *e.NPlusOne { buf = append(buf, 1) } else { buf = append(buf, 0) }
	}
	writeUvar(FlagSlowQueries, e.SlowQueries)
	writeUvar(FlagDupQueries, e.DupQueries)
	writeStr(FlagErrorClass, e.ErrorClass)
	writeStr(FlagErrorMessage, e.ErrorMessage)
	writeStr(FlagSourceFile, e.SourceFile)
	writeUvar(FlagSourceLine, e.SourceLine)
	writeStr(FlagErrFingerprint, e.ErrorFingerprint)
	writeStr(FlagJobClass, e.JobClass)
	writeStr(FlagJobQueue, e.JobQueue)
	writeStr(FlagJobID, e.JobID)
	writeUvar(FlagQueueMs, e.QueueMs)

	if flags&FlagBody != 0 {
		compressed := enc.Compress(e.Body)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(compressed)))
		buf = append(buf, compressed...)
	}

	binary.LittleEndian.PutUint32(buf[:4], uint32(len(buf)-4))
	return buf
}

// UnmarshalEntry deserializes a chunk.Entry from compact binary format.
func UnmarshalEntry(data []byte) (*chunk.Entry, error) {
	if len(data) < 33 { // id(8) + ts(8) + received_at(8) + level(1) + flags(8)
		return nil, fmt.Errorf("entry data too short: %d bytes", len(data))
	}

	e := &chunk.Entry{}
	off := 0

	e.ID = int64(binary.LittleEndian.Uint64(data[off:])); off += 8
	e.Ts = int64(binary.LittleEndian.Uint64(data[off:])); off += 8
	e.ReceivedAt = int64(binary.LittleEndian.Uint64(data[off:])); off += 8

	lvlEnum := data[off]; off++
	if int(lvlEnum) < len(enumToLevel) { e.Level = enumToLevel[lvlEnum] } else { e.Level = "info" }

	flags := binary.LittleEndian.Uint64(data[off:]); off += 8

	var err error
	e.Service, off, err = readString(data, off)
	if err != nil { return nil, fmt.Errorf("read service: %w", err) }
	e.Message, off, err = readString(data, off)
	if err != nil { return nil, fmt.Errorf("read message: %w", err) }

	// String reader helper
	rStr := func(f uint64, dest *string, name string) error {
		if flags&f == 0 { return nil }
		var v string
		var e error
		v, off, e = readString(data, off)
		if e != nil { return fmt.Errorf("read %s: %w", name, e) }
		*dest = v
		return nil
	}
	// Uvarint reader helper
	rUvar := func(f uint64, dest *int, name string) error {
		if flags&f == 0 { return nil }
		v, n := binary.Uvarint(data[off:])
		if n <= 0 { return fmt.Errorf("read %s: invalid uvarint", name) }
		*dest = int(v); off += n
		return nil
	}

	if err = rStr(FlagEnv, &e.Env, "env"); err != nil { return nil, err }
	if err = rStr(FlagVersion, &e.Version, "version"); err != nil { return nil, err }
	if err = rStr(FlagHost, &e.Host, "host"); err != nil { return nil, err }
	if err = rStr(FlagKind, &e.Kind, "kind"); err != nil { return nil, err }
	if err = rStr(FlagEventType, &e.EventType, "event_type"); err != nil { return nil, err }
	if err = rStr(FlagTraceID, &e.TraceID, "trace_id"); err != nil { return nil, err }
	if err = rStr(FlagSpanID, &e.SpanID, "span_id"); err != nil { return nil, err }
	if err = rStr(FlagParentSpanID, &e.ParentSpanID, "parent_span_id"); err != nil { return nil, err }
	if err = rStr(FlagRequestID, &e.RequestID, "request_id"); err != nil { return nil, err }
	if err = rStr(FlagUserID, &e.UserID, "user_id"); err != nil { return nil, err }
	if err = rStr(FlagTenantID, &e.TenantID, "tenant_id"); err != nil { return nil, err }
	if err = rStr(FlagSessionID, &e.SessionID, "session_id"); err != nil { return nil, err }
	if err = rStr(FlagMethod, &e.Method, "method"); err != nil { return nil, err }
	if err = rStr(FlagPath, &e.Path, "path"); err != nil { return nil, err }
	if err = rStr(FlagRoute, &e.Route, "route"); err != nil { return nil, err }
	if err = rStr(FlagHandler, &e.Handler, "handler"); err != nil { return nil, err }

	if flags&FlagStatus != 0 {
		if off+2 > len(data) { return nil, fmt.Errorf("read status: truncated") }
		e.Status = int(binary.LittleEndian.Uint16(data[off:])); off += 2
	}

	if err = rUvar(FlagDurationMs, &e.DurationMs, "duration_ms"); err != nil { return nil, err }
	if err = rUvar(FlagDbMs, &e.DbMs, "db_ms"); err != nil { return nil, err }
	if err = rUvar(FlagDbCount, &e.DbCount, "db_count"); err != nil { return nil, err }
	if err = rUvar(FlagCacheMs, &e.CacheMs, "cache_ms"); err != nil { return nil, err }
	if err = rUvar(FlagCacheHits, &e.CacheHits, "cache_hits"); err != nil { return nil, err }
	if err = rUvar(FlagCacheMisses, &e.CacheMisses, "cache_misses"); err != nil { return nil, err }
	if err = rUvar(FlagExtMs, &e.ExtMs, "ext_ms"); err != nil { return nil, err }
	if err = rUvar(FlagExtCount, &e.ExtCount, "ext_count"); err != nil { return nil, err }
	if err = rUvar(FlagRenderMs, &e.RenderMs, "render_ms"); err != nil { return nil, err }
	if err = rUvar(FlagAllocCount, &e.AllocCount, "alloc_count"); err != nil { return nil, err }
	if err = rUvar(FlagMemDeltaMb, &e.MemDeltaMb, "mem_delta_mb"); err != nil { return nil, err }

	if flags&FlagNPlusOne != 0 {
		if off >= len(data) { return nil, fmt.Errorf("read n_plus_one: truncated") }
		v := data[off] != 0; e.NPlusOne = &v; off++
	}

	if err = rUvar(FlagSlowQueries, &e.SlowQueries, "slow_queries"); err != nil { return nil, err }
	if err = rUvar(FlagDupQueries, &e.DupQueries, "dup_queries"); err != nil { return nil, err }
	if err = rStr(FlagErrorClass, &e.ErrorClass, "error_class"); err != nil { return nil, err }
	if err = rStr(FlagErrorMessage, &e.ErrorMessage, "error_message"); err != nil { return nil, err }
	if err = rStr(FlagSourceFile, &e.SourceFile, "source_file"); err != nil { return nil, err }
	if err = rUvar(FlagSourceLine, &e.SourceLine, "source_line"); err != nil { return nil, err }
	if err = rStr(FlagErrFingerprint, &e.ErrorFingerprint, "error_fingerprint"); err != nil { return nil, err }
	if err = rStr(FlagJobClass, &e.JobClass, "job_class"); err != nil { return nil, err }
	if err = rStr(FlagJobQueue, &e.JobQueue, "job_queue"); err != nil { return nil, err }
	if err = rStr(FlagJobID, &e.JobID, "job_id"); err != nil { return nil, err }
	if err = rUvar(FlagQueueMs, &e.QueueMs, "queue_ms"); err != nil { return nil, err }

	if flags&FlagBody != 0 {
		if off+4 > len(data) { return nil, fmt.Errorf("read body length: truncated") }
		bodyLen := int(binary.LittleEndian.Uint32(data[off:])); off += 4
		if off+bodyLen > len(data) { return nil, fmt.Errorf("read body: truncated") }
		decompressed, err := enc.Decompress(data[off : off+bodyLen])
		if err != nil { return nil, fmt.Errorf("decompress body: %w", err) }
		e.Body = decompressed; off += bodyLen
	}

	_ = off
	return e, nil
}

// ReadEntries reads all entries from a WAL file (reader positioned at start).
func ReadEntries(r io.Reader) ([]chunk.Entry, error) {
	var entries []chunk.Entry
	lenBuf := make([]byte, 4)

	for {
		_, err := io.ReadFull(r, lenBuf)
		if err == io.EOF { break }
		if err != nil { return entries, fmt.Errorf("read entry length: %w", err) }

		entryLen := int(binary.LittleEndian.Uint32(lenBuf))
		if entryLen <= 0 || entryLen > 10<<20 { return entries, fmt.Errorf("invalid entry length: %d", entryLen) }

		entryData := make([]byte, entryLen)
		if _, err = io.ReadFull(r, entryData); err != nil { return entries, fmt.Errorf("read entry data: %w", err) }

		entry, err := UnmarshalEntry(entryData)
		if err != nil { return entries, fmt.Errorf("unmarshal entry: %w", err) }
		entries = append(entries, *entry)
	}

	return entries, nil
}

func appendString(buf []byte, s string) []byte {
	buf = binary.LittleEndian.AppendUint16(buf, uint16(len(s)))
	return append(buf, s...)
}

func readString(data []byte, off int) (string, int, error) {
	if off+2 > len(data) { return "", off, fmt.Errorf("truncated string length at offset %d", off) }
	sLen := int(binary.LittleEndian.Uint16(data[off:])); off += 2
	if off+sLen > len(data) { return "", off, fmt.Errorf("truncated string data at offset %d", off) }
	return string(data[off : off+sLen]), off + sLen, nil
}
