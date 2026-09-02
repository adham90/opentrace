package wal

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"unicode/utf8"

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
	FlagErrorClass     uint64 = 1 << 31
	FlagErrorMessage   uint64 = 1 << 32
	FlagSourceFile     uint64 = 1 << 33
	FlagSourceLine     uint64 = 1 << 34
	FlagErrFingerprint uint64 = 1 << 35

	// Job
	FlagJobClass uint64 = 1 << 36
	FlagJobQueue uint64 = 1 << 37
	FlagJobID    uint64 = 1 << 38
	FlagQueueMs  uint64 = 1 << 39

	// Body
	FlagBody uint64 = 1 << 40

	// --- Record format markers (high bits, never used for fields) ---

	// FlagExtendedStrings marks a record written with the extended string
	// framing (enc.LenExtended) and the custom-level escape. It is the WAL's
	// format version: records without it were written by an older build that
	// framed strings with a bare uint16 length, so they are decoded with
	// enc.LenUint16 and keep reading correctly.
	FlagExtendedStrings uint64 = 1 << 63

	// FlagRawBody marks a body stored without the transient WAL zstd pass. The
	// sealed body column is compressed later, so a tiny deployment can avoid
	// compressing at ingest only to decompress and recompress during sealing.
	FlagRawBody uint64 = 1 << 62
)

// CompressBodies controls the representation of bodies in newly written WAL
// records. Configure it before opening a writer. Both representations are
// self-describing and readable by this version.
var CompressBodies = true

// Level enum values (1 byte in binary format).
var levelToEnum = map[string]byte{
	"trace": 0, "debug": 1, "info": 2, "warn": 3, "warning": 3, "error": 4, "fatal": 5,
}

var enumToLevel = []string{"trace", "debug", "info", "warn", "error", "fatal"}

const (
	// levelEnumInfo is the fallback level for records whose enum is unknown.
	levelEnumInfo byte = 2
	// levelEnumCustom marks a level outside levelToEnum. The literal level
	// string then follows the flags word, so unusual levels ("critical",
	// "notice", …) round-trip instead of being silently rewritten to "info" —
	// which made the live (ring buffer) and sealed views of one entry disagree.
	levelEnumCustom byte = 0xFF

	// MaxFieldBytes caps any single string field written to the WAL. Nothing
	// upstream limits field length (only a 10MB HTTP body), and an unbounded
	// field would let one entry blow past maxRecordBytes and wedge the segment.
	// Longer values are truncated at a UTF-8 boundary, never misframed.
	MaxFieldBytes = 1 << 20 // 1 MiB

	// maxCompressedBodyBytes caps the compressed body blob. A body larger than
	// this is dropped rather than written, so records stay under maxRecordBytes.
	maxCompressedBodyBytes = 8 << 20 // 8 MiB

	// maxRecordBytes is the largest record ReadEntries will accept. It must
	// exceed anything MarshalEntry can emit (string fields + body + framing),
	// otherwise the writer could produce records its own reader rejects.
	maxRecordBytes = 48 << 20 // 48 MiB

	// minRecordBytes is the fixed prefix: id(8) + ts(8) + received_at(8) +
	// level(1) + flags(8).
	minRecordBytes = 33
)

// MarshalEntry serializes a chunk.Entry to compact binary format.
// Format: [4 bytes total_len] [8 bytes id] [8 bytes ts] [8 bytes received_at]
//
//	[1 byte level_enum] [8 bytes flags] [level string, if level_enum == 0xFF]
//	[service] [message] [optional fields based on flags]
//
// Strings use the extended length framing (enc.LenExtended): a uint16 length
// where 0xFFFF escapes to a following uint32. FlagExtendedStrings is set on
// every record this build writes; records without it are read back with the
// legacy uint16 framing, so WAL files written by older builds still replay.
// Individual fields are capped at MaxFieldBytes.
func MarshalEntry(e *chunk.Entry) []byte {
	return AppendEntry(make([]byte, 0, 256), e)
}

// AppendEntry marshals e onto buf and returns the extended slice, so a batch can
// be serialized into one buffer and written with a single syscall.
func AppendEntry(buf []byte, e *chunk.Entry) []byte {
	// Where this record starts, so the length prefix is patched in the right
	// place when buf already holds earlier records.
	recStart := len(buf)

	// Placeholder for total length (filled at end)
	buf = append(buf, 0, 0, 0, 0)

	// Fixed fields
	buf = binary.LittleEndian.AppendUint64(buf, uint64(e.ID))
	buf = binary.LittleEndian.AppendUint64(buf, uint64(e.Ts))
	buf = binary.LittleEndian.AppendUint64(buf, uint64(e.ReceivedAt))

	lvl, knownLevel := levelToEnum[e.Level]
	if !knownLevel {
		lvl = levelEnumCustom
	}
	buf = append(buf, lvl)

	// Compute flags (uint64)
	flags := FlagExtendedStrings
	setStr := func(f uint64, v string) {
		if v != "" {
			flags |= f
		}
	}
	setInt := func(f uint64, v int) {
		if v > 0 {
			flags |= f
		}
	}

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
	if e.NPlusOne != nil {
		flags |= FlagNPlusOne
	}
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
	var body []byte
	if len(e.Body) > 0 {
		if CompressBodies {
			body = enc.Compress(e.Body)
		} else {
			body = e.Body
			flags |= FlagRawBody
		}
		if len(body) > maxCompressedBodyBytes {
			slog.Warn("wal: dropping oversized entry body",
				"id", e.ID, "compressed_bytes", len(body), "max", maxCompressedBodyBytes)
			body = nil
		} else {
			flags |= FlagBody
		}
	}

	buf = binary.LittleEndian.AppendUint64(buf, flags)

	// A level outside the enum is stored verbatim right after the flags.
	if !knownLevel {
		buf = appendString(buf, e.Level)
	}

	// Required string fields
	buf = appendString(buf, e.Service)
	buf = appendString(buf, e.Message)

	// Optional string fields
	writeStr := func(f uint64, v string) {
		if flags&f != 0 {
			buf = appendString(buf, v)
		}
	}
	writeUvar := func(f uint64, v int) {
		if flags&f != 0 {
			buf = enc.PutUvarint(buf, uint64(v))
		}
	}

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
		if *e.NPlusOne {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
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
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(body)))
		buf = append(buf, body...)
	}

	binary.LittleEndian.PutUint32(buf[recStart:recStart+4], uint32(len(buf)-recStart-4))
	return buf
}

// UnmarshalEntry deserializes a chunk.Entry from compact binary format.
func UnmarshalEntry(data []byte) (*chunk.Entry, error) {
	e := &chunk.Entry{}
	if err := UnmarshalEntryInto(data, e); err != nil {
		return nil, err
	}
	return e, nil
}

// UnmarshalEntryInto is UnmarshalEntry writing into an existing Entry, so a
// scan can fill a preallocated slice instead of allocating a 600-byte struct
// per record.
//
// Nothing in the decoded entry aliases data: strings are copied by
// LenFormat.ReadString and the body comes back from Decompress as its own
// slice. That is what lets a caller reuse one record buffer for a whole file.
func UnmarshalEntryInto(data []byte, e *chunk.Entry) error {
	if len(data) < minRecordBytes {
		return fmt.Errorf("entry data too short: %d bytes", len(data))
	}

	*e = chunk.Entry{}
	off := 0

	e.ID = int64(binary.LittleEndian.Uint64(data[off:]))
	off += 8
	e.Ts = int64(binary.LittleEndian.Uint64(data[off:]))
	off += 8
	e.ReceivedAt = int64(binary.LittleEndian.Uint64(data[off:]))
	off += 8

	lvlEnum := data[off]
	off++

	flags := binary.LittleEndian.Uint64(data[off:])
	off += 8

	// Records written before the extended framing used a bare uint16 prefix.
	lenFmt := enc.LenUint16
	if flags&FlagExtendedStrings != 0 {
		lenFmt = enc.LenExtended
	}
	readString := func(data []byte, off int) (string, int, error) {
		return lenFmt.ReadString(data, off)
	}

	var err error
	switch {
	case lvlEnum == levelEnumCustom && flags&FlagExtendedStrings != 0:
		e.Level, off, err = readString(data, off)
		if err != nil {
			return fmt.Errorf("read level: %w", err)
		}
	case int(lvlEnum) < len(enumToLevel):
		e.Level = enumToLevel[lvlEnum]
	default:
		e.Level = enumToLevel[levelEnumInfo]
	}

	e.Service, off, err = readString(data, off)
	if err != nil {
		return fmt.Errorf("read service: %w", err)
	}
	e.Message, off, err = readString(data, off)
	if err != nil {
		return fmt.Errorf("read message: %w", err)
	}

	// String reader helper
	rStr := func(f uint64, dest *string, name string) error {
		if flags&f == 0 {
			return nil
		}
		var v string
		var e error
		v, off, e = readString(data, off)
		if e != nil {
			return fmt.Errorf("read %s: %w", name, e)
		}
		*dest = v
		return nil
	}
	// Uvarint reader helper
	rUvar := func(f uint64, dest *int, name string) error {
		if flags&f == 0 {
			return nil
		}
		v, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return fmt.Errorf("read %s: invalid uvarint", name)
		}
		*dest = int(v)
		off += n
		return nil
	}

	if err = rStr(FlagEnv, &e.Env, "env"); err != nil {
		return err
	}
	if err = rStr(FlagVersion, &e.Version, "version"); err != nil {
		return err
	}
	if err = rStr(FlagHost, &e.Host, "host"); err != nil {
		return err
	}
	if err = rStr(FlagKind, &e.Kind, "kind"); err != nil {
		return err
	}
	if err = rStr(FlagEventType, &e.EventType, "event_type"); err != nil {
		return err
	}
	if err = rStr(FlagTraceID, &e.TraceID, "trace_id"); err != nil {
		return err
	}
	if err = rStr(FlagSpanID, &e.SpanID, "span_id"); err != nil {
		return err
	}
	if err = rStr(FlagParentSpanID, &e.ParentSpanID, "parent_span_id"); err != nil {
		return err
	}
	if err = rStr(FlagRequestID, &e.RequestID, "request_id"); err != nil {
		return err
	}
	if err = rStr(FlagUserID, &e.UserID, "user_id"); err != nil {
		return err
	}
	if err = rStr(FlagTenantID, &e.TenantID, "tenant_id"); err != nil {
		return err
	}
	if err = rStr(FlagSessionID, &e.SessionID, "session_id"); err != nil {
		return err
	}
	if err = rStr(FlagMethod, &e.Method, "method"); err != nil {
		return err
	}
	if err = rStr(FlagPath, &e.Path, "path"); err != nil {
		return err
	}
	if err = rStr(FlagRoute, &e.Route, "route"); err != nil {
		return err
	}
	if err = rStr(FlagHandler, &e.Handler, "handler"); err != nil {
		return err
	}

	if flags&FlagStatus != 0 {
		if off+2 > len(data) {
			return fmt.Errorf("read status: truncated")
		}
		e.Status = int(binary.LittleEndian.Uint16(data[off:]))
		off += 2
	}

	if err = rUvar(FlagDurationMs, &e.DurationMs, "duration_ms"); err != nil {
		return err
	}
	if err = rUvar(FlagDbMs, &e.DbMs, "db_ms"); err != nil {
		return err
	}
	if err = rUvar(FlagDbCount, &e.DbCount, "db_count"); err != nil {
		return err
	}
	if err = rUvar(FlagCacheMs, &e.CacheMs, "cache_ms"); err != nil {
		return err
	}
	if err = rUvar(FlagCacheHits, &e.CacheHits, "cache_hits"); err != nil {
		return err
	}
	if err = rUvar(FlagCacheMisses, &e.CacheMisses, "cache_misses"); err != nil {
		return err
	}
	if err = rUvar(FlagExtMs, &e.ExtMs, "ext_ms"); err != nil {
		return err
	}
	if err = rUvar(FlagExtCount, &e.ExtCount, "ext_count"); err != nil {
		return err
	}
	if err = rUvar(FlagRenderMs, &e.RenderMs, "render_ms"); err != nil {
		return err
	}
	if err = rUvar(FlagAllocCount, &e.AllocCount, "alloc_count"); err != nil {
		return err
	}
	if err = rUvar(FlagMemDeltaMb, &e.MemDeltaMb, "mem_delta_mb"); err != nil {
		return err
	}

	if flags&FlagNPlusOne != 0 {
		if off >= len(data) {
			return fmt.Errorf("read n_plus_one: truncated")
		}
		v := data[off] != 0
		e.NPlusOne = &v
		off++
	}

	if err = rUvar(FlagSlowQueries, &e.SlowQueries, "slow_queries"); err != nil {
		return err
	}
	if err = rUvar(FlagDupQueries, &e.DupQueries, "dup_queries"); err != nil {
		return err
	}
	if err = rStr(FlagErrorClass, &e.ErrorClass, "error_class"); err != nil {
		return err
	}
	if err = rStr(FlagErrorMessage, &e.ErrorMessage, "error_message"); err != nil {
		return err
	}
	if err = rStr(FlagSourceFile, &e.SourceFile, "source_file"); err != nil {
		return err
	}
	if err = rUvar(FlagSourceLine, &e.SourceLine, "source_line"); err != nil {
		return err
	}
	if err = rStr(FlagErrFingerprint, &e.ErrorFingerprint, "error_fingerprint"); err != nil {
		return err
	}
	if err = rStr(FlagJobClass, &e.JobClass, "job_class"); err != nil {
		return err
	}
	if err = rStr(FlagJobQueue, &e.JobQueue, "job_queue"); err != nil {
		return err
	}
	if err = rStr(FlagJobID, &e.JobID, "job_id"); err != nil {
		return err
	}
	if err = rUvar(FlagQueueMs, &e.QueueMs, "queue_ms"); err != nil {
		return err
	}

	if flags&FlagBody != 0 {
		if off+4 > len(data) {
			return fmt.Errorf("read body length: truncated")
		}
		bodyLen := int(binary.LittleEndian.Uint32(data[off:]))
		off += 4
		if off+bodyLen > len(data) {
			return fmt.Errorf("read body: truncated")
		}
		encodedBody := data[off : off+bodyLen]
		if flags&FlagRawBody != 0 {
			e.Body = append(e.Body[:0], encodedBody...)
		} else {
			decompressed, err := enc.Decompress(encodedBody)
			if err != nil {
				return fmt.Errorf("decompress body: %w", err)
			}
			e.Body = decompressed
		}
		off += bodyLen
	}

	// Every byte must be accounted for. Without this, a misframed record (the
	// pre-fix uint16 length wrap) could parse into silently garbled fields
	// instead of reporting corruption.
	if off != len(data) {
		return fmt.Errorf("entry has %d trailing bytes after %d parsed", len(data)-off, off)
	}
	return nil
}

// ReadEntries reads all entries from a WAL file (reader positioned at start).
func ReadEntries(r io.Reader) ([]chunk.Entry, error) {
	entries, _, err := ReadEntriesFrom(r)
	return entries, err
}

// ReadEntriesFrom is ReadEntries that also reports how many bytes were consumed
// by fully-parsed records. A WAL is append-only, so a caller that remembers the
// offset can re-read only what was appended since instead of re-parsing the
// whole file. A torn trailing record does not advance the offset, so it is
// re-read (and completed) on the next pass rather than skipped.
func ReadEntriesFrom(r io.Reader) ([]chunk.Entry, int64, error) {
	var entries []chunk.Entry
	sc := NewScanner(r)
	for {
		var e chunk.Entry
		if !sc.Next(&e) {
			break
		}
		entries = append(entries, e)
	}
	return entries, sc.Consumed(), sc.Err()
}

// appendString writes s with the extended length framing, capped at
// MaxFieldBytes. The old code wrote uint16(len(s)) and then all the bytes, so a
// field over 65535 bytes desynchronized the record and corrupted every record
// after it in the WAL.
func appendString(buf []byte, s string) []byte {
	return enc.AppendLenString(buf, truncateField(s))
}

// truncateField clamps s to MaxFieldBytes on a UTF-8 rune boundary.
func truncateField(s string) string {
	if len(s) <= MaxFieldBytes {
		return s
	}
	cut := MaxFieldBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	slog.Warn("wal: truncating oversized field", "bytes", len(s), "max", MaxFieldBytes)
	return s[:cut]
}
