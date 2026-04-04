package chunk

import "encoding/json"

// ColumnType identifies how a column's values are encoded.
type ColumnType uint8

const (
	ColDeltaVarint    ColumnType = 1  // int64 delta + varint + zstd (id, received_at)
	ColZstdInt64      ColumnType = 2  // int64 raw LE + zstd (ts — may not be monotonic)
	ColDictBitpack    ColumnType = 3  // dictionary + bitpacked indices (level)
	ColDictZstd       ColumnType = 4  // dictionary + zstd indices (service, env, method, etc.)
	ColZstdBlock      ColumnType = 5  // length-prefixed strings + zstd (message)
	ColSparseString   ColumnType = 6  // null bitmap + strings + zstd (trace_id, path, etc.)
	ColSparseInt64    ColumnType = 7  // null bitmap + varint int64 + zstd (duration_ms, db_ms, etc.)
	ColSparseBool     ColumnType = 8  // null bitmap + value bitmap (n_plus_one)
	ColSparseBytes    ColumnType = 9  // null bitmap + length-prefixed bytes + zstd (body)
	ColSparseDictZstd ColumnType = 10 // null bitmap + dictionary + zstd (event_type, handler, etc.)
)

// ColumnDef defines a column in the schema.
type ColumnDef struct {
	Name string
	Type ColumnType
}

// Schema is the ordered list of columns in a chunk (45 columns).
// Column order matches the order they're written in the file.
var Schema = []ColumnDef{
	// --- Always present ---
	{Name: "id", Type: ColDeltaVarint},
	{Name: "ts", Type: ColZstdInt64},
	{Name: "received_at", Type: ColDeltaVarint},
	{Name: "level", Type: ColDictBitpack},
	{Name: "service", Type: ColDictZstd},
	{Name: "message", Type: ColZstdBlock},

	// --- Common context ---
	{Name: "env", Type: ColDictZstd},
	{Name: "version", Type: ColSparseDictZstd},
	{Name: "host", Type: ColSparseDictZstd},
	{Name: "kind", Type: ColDictZstd}, // log, request, job, event
	{Name: "event_type", Type: ColSparseDictZstd},
	{Name: "trace_id", Type: ColSparseString},
	{Name: "span_id", Type: ColSparseString},
	{Name: "parent_span_id", Type: ColSparseString},
	{Name: "request_id", Type: ColSparseString},
	{Name: "user_id", Type: ColSparseString},
	{Name: "tenant_id", Type: ColSparseDictZstd},
	{Name: "session_id", Type: ColSparseString},

	// --- HTTP request (kind=request) ---
	{Name: "method", Type: ColSparseDictZstd},
	{Name: "path", Type: ColSparseString},
	{Name: "route", Type: ColSparseDictZstd},
	{Name: "handler", Type: ColSparseDictZstd},
	{Name: "status", Type: ColSparseDictZstd},
	{Name: "duration_ms", Type: ColSparseInt64},

	// --- Performance (kind=request or kind=job) ---
	{Name: "db_ms", Type: ColSparseInt64},
	{Name: "db_count", Type: ColSparseInt64},
	{Name: "cache_ms", Type: ColSparseInt64},
	{Name: "cache_hits", Type: ColSparseInt64},
	{Name: "cache_misses", Type: ColSparseInt64},
	{Name: "ext_ms", Type: ColSparseInt64},
	{Name: "ext_count", Type: ColSparseInt64},
	{Name: "render_ms", Type: ColSparseInt64},
	{Name: "alloc_count", Type: ColSparseInt64},
	{Name: "mem_delta_mb", Type: ColSparseInt64}, // stored as int: value * 100 (2 decimal places)
	{Name: "n_plus_one", Type: ColSparseBool},
	{Name: "slow_queries", Type: ColSparseInt64},
	{Name: "dup_queries", Type: ColSparseInt64},

	// --- Error tracking (level=error/fatal) ---
	{Name: "error_class", Type: ColSparseDictZstd},
	{Name: "error_message", Type: ColSparseString},
	{Name: "source_file", Type: ColSparseString},
	{Name: "source_line", Type: ColSparseInt64},
	{Name: "error_fingerprint", Type: ColSparseString},

	// --- Job (kind=job) ---
	{Name: "job_class", Type: ColSparseDictZstd},
	{Name: "job_queue", Type: ColSparseDictZstd},
	{Name: "job_id", Type: ColSparseString},
	{Name: "queue_ms", Type: ColSparseInt64},

	// --- Body (opaque blob) ---
	{Name: "body", Type: ColSparseBytes},
}

// Entry is a single log entry with all fields.
// Flat structure matching the SDK wire format + server-assigned fields.
type Entry struct {
	// --- Always present ---
	ID         int64  `json:"id"`
	Ts         int64  `json:"ts"`          // epoch ms, event time from SDK
	ReceivedAt int64  `json:"received_at"` // epoch ms, server receive time
	Level      string `json:"level"`
	Service    string `json:"service"`
	Message    string `json:"message"`

	// --- Common context ---
	Env          string `json:"env,omitempty"`
	Version      string `json:"version,omitempty"`
	Host         string `json:"host,omitempty"`
	Kind         string `json:"kind,omitempty"`       // log, request, job, event
	EventType    string `json:"event_type,omitempty"`
	TraceID      string `json:"trace_id,omitempty"`
	SpanID       string `json:"span_id,omitempty"`
	ParentSpanID string `json:"parent_span_id,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	TenantID     string `json:"tenant_id,omitempty"`
	SessionID    string `json:"session_id,omitempty"`

	// --- HTTP request ---
	Method     string `json:"method,omitempty"`
	Path       string `json:"path,omitempty"`
	Route      string `json:"route,omitempty"`
	Handler    string `json:"handler,omitempty"` // framework-agnostic: "OrdersController#create", "createOrder", etc.
	Status     int    `json:"status,omitempty"`
	DurationMs int    `json:"duration_ms,omitempty"`

	// --- Performance ---
	DbMs        int   `json:"db_ms,omitempty"`
	DbCount     int   `json:"db_count,omitempty"`
	CacheMs     int   `json:"cache_ms,omitempty"`
	CacheHits   int   `json:"cache_hits,omitempty"`
	CacheMisses int   `json:"cache_misses,omitempty"`
	ExtMs       int   `json:"ext_ms,omitempty"`
	ExtCount    int   `json:"ext_count,omitempty"`
	RenderMs    int   `json:"render_ms,omitempty"`
	AllocCount  int   `json:"alloc_count,omitempty"`
	MemDeltaMb  int   `json:"mem_delta_mb,omitempty"` // value * 100 (e.g., 170 = 1.70 MB)
	NPlusOne    *bool `json:"n_plus_one,omitempty"`
	SlowQueries int   `json:"slow_queries,omitempty"`
	DupQueries  int   `json:"dup_queries,omitempty"`

	// --- Error tracking ---
	ErrorClass      string `json:"error_class,omitempty"`
	ErrorMessage    string `json:"error_message,omitempty"`
	SourceFile      string `json:"source_file,omitempty"`
	SourceLine      int    `json:"source_line,omitempty"`
	ErrorFingerprint string `json:"error_fingerprint,omitempty"` // server-computed

	// --- Job ---
	JobClass string `json:"job_class,omitempty"`
	JobQueue string `json:"job_queue,omitempty"`
	JobID    string `json:"job_id,omitempty"`
	QueueMs  int    `json:"queue_ms,omitempty"`

	// --- Body (opaque blob) ---
	Body json.RawMessage `json:"body,omitempty"`
}

// ColumnIndex maps column name to its position in Schema.
var ColumnIndex map[string]int

func init() {
	ColumnIndex = make(map[string]int, len(Schema))
	for i, col := range Schema {
		ColumnIndex[col.Name] = i
	}
}
