package chunk

import "encoding/json"

// ColumnType identifies how a column's values are encoded.
type ColumnType uint8

const (
	ColDeltaVarint   ColumnType = 1 // int64 delta + varint + zstd (id, received_at)
	ColZstdInt64     ColumnType = 2 // int64 raw LE + zstd (ts — may not be monotonic)
	ColDictBitpack   ColumnType = 3 // dictionary + bitpacked indices (level)
	ColDictZstd      ColumnType = 4 // dictionary + zstd indices (service, env, method, etc.)
	ColZstdBlock     ColumnType = 5 // length-prefixed strings + zstd (message)
	ColSparseString  ColumnType = 6 // null bitmap + strings + zstd (trace_id, path, etc.)
	ColSparseInt64   ColumnType = 7 // null bitmap + varint int64 + zstd (duration_ms, db_ms, etc.)
	ColSparseBool    ColumnType = 8 // null bitmap + value bitmap (n_plus_one)
	ColSparseBytes   ColumnType = 9 // null bitmap + length-prefixed bytes + zstd (body)
	ColSparseDictZstd ColumnType = 10 // null bitmap + dictionary + zstd (event_type, controller, etc.)
)

// ColumnDef defines a column in the schema.
type ColumnDef struct {
	Name string
	Type ColumnType
}

// Schema is the ordered list of columns in a chunk.
// Column order matches the order they're written in the file.
var Schema = []ColumnDef{
	{Name: "id", Type: ColDeltaVarint},
	{Name: "ts", Type: ColZstdInt64},
	{Name: "received_at", Type: ColDeltaVarint},
	{Name: "level", Type: ColDictBitpack},
	{Name: "service", Type: ColDictZstd},
	{Name: "env", Type: ColDictZstd},
	{Name: "version", Type: ColSparseDictZstd},
	{Name: "message", Type: ColZstdBlock},
	{Name: "event_type", Type: ColSparseDictZstd},
	{Name: "trace_id", Type: ColSparseString},
	{Name: "span_id", Type: ColSparseString},
	{Name: "parent_span_id", Type: ColSparseString},
	{Name: "request_id", Type: ColSparseString},
	{Name: "user_id", Type: ColSparseString},
	{Name: "tenant_id", Type: ColSparseDictZstd},
	{Name: "session_id", Type: ColSparseString},
	{Name: "method", Type: ColSparseDictZstd},
	{Name: "path", Type: ColSparseString},
	{Name: "status", Type: ColSparseDictZstd},
	{Name: "duration_ms", Type: ColSparseInt64},
	{Name: "controller", Type: ColSparseDictZstd},
	{Name: "action", Type: ColSparseDictZstd},
	{Name: "db_ms", Type: ColSparseInt64},
	{Name: "db_count", Type: ColSparseInt64},
	{Name: "n_plus_one", Type: ColSparseBool},
	{Name: "slow_queries", Type: ColSparseInt64},
	{Name: "dup_queries", Type: ColSparseInt64},
	{Name: "exception_class", Type: ColSparseDictZstd},
	{Name: "source_file", Type: ColSparseString},
	{Name: "source_line", Type: ColSparseInt64},
	{Name: "error_fingerprint", Type: ColSparseString},
	{Name: "body", Type: ColSparseBytes},
}

// Entry is a single log entry with all 32 fields.
// Flat structure matching the SDK wire format + server-extracted fields.
type Entry struct {
	ID               int64           `json:"id"`
	Ts               int64           `json:"ts"`                          // epoch ms, event time from SDK
	ReceivedAt       int64           `json:"received_at"`                 // epoch ms, server receive time
	Level            string          `json:"level"`
	Service          string          `json:"service"`
	Env              string          `json:"env,omitempty"`
	Version          string          `json:"version,omitempty"`
	Message          string          `json:"message"`
	EventType        string          `json:"event_type,omitempty"`
	TraceID          string          `json:"trace_id,omitempty"`
	SpanID           string          `json:"span_id,omitempty"`
	ParentSpanID     string          `json:"parent_span_id,omitempty"`
	RequestID        string          `json:"request_id,omitempty"`
	UserID           string          `json:"user_id,omitempty"`
	TenantID         string          `json:"tenant_id,omitempty"`
	SessionID        string          `json:"session_id,omitempty"`
	Method           string          `json:"method,omitempty"`
	Path             string          `json:"path,omitempty"`
	Status           int             `json:"status,omitempty"`
	DurationMs       int             `json:"duration_ms,omitempty"`
	Controller       string          `json:"controller,omitempty"`
	Action           string          `json:"action,omitempty"`
	DbMs             int             `json:"db_ms,omitempty"`
	DbCount          int             `json:"db_count,omitempty"`
	NPlusOne         *bool           `json:"n_plus_one,omitempty"`
	SlowQueries      int             `json:"slow_queries,omitempty"`
	DupQueries       int             `json:"dup_queries,omitempty"`
	ExceptionClass   string          `json:"exception_class,omitempty"`   // server-extracted
	SourceFile       string          `json:"source_file,omitempty"`       // server-extracted
	SourceLine       int             `json:"source_line,omitempty"`       // server-extracted
	ErrorFingerprint string          `json:"error_fingerprint,omitempty"` // server-computed
	Body             json.RawMessage `json:"body,omitempty"`              // opaque JSON blob
}

// ColumnIndex maps column name to its position in Schema.
var ColumnIndex map[string]int

func init() {
	ColumnIndex = make(map[string]int, len(Schema))
	for i, col := range Schema {
		ColumnIndex[col.Name] = i
	}
}
