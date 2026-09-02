package engine

import (
	"bufio"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/wal"
)

// WALCacheMaxEntries caps how many parsed entries one WAL file may keep cached.
// Past it the file is parsed per query as before, so a pathologically busy hour
// cannot pin an unbounded slice for the life of the process.
//
// At the default this is roughly 120 MB of entry structs plus their bodies, held
// only for the current hour — the cache is dropped when the hour seals. Lower it
// (or set it to 0) on a memory-constrained box; the cost is that every query
// touching the live hour re-parses the WAL from disk, which is what it did
// before the cache existed.
//
// ponytail: one flat cap across the whole cache would be better accounting, but
// there is only ever the live WAL plus the (usually zero) in-flight seals in
// here, so a per-file cap is the same number in practice.
var WALCacheMaxEntries = 200_000

// WALCacheBytes is the process-wide budget for parsed live WAL entries. Entry
// count is not a memory budget: two batches with the same count can differ by
// gigabytes when bodies and messages differ. The legacy entry cap remains as a
// second guard and for compatibility with the existing environment variable.
var WALCacheBytes int64 = 64 << 20

// walCache holds the parsed contents of the unsealed WAL files.
//
// Every query that touches the live hour scans those files, and they were
// re-opened and re-parsed from byte zero each time — a single MCP tool call
// runs several queries, so a busy hour was parsed several times over per call.
// WAL files are append-only, so a cached parse stays valid and only the bytes
// appended since need reading.
//
// Identity is checked with os.SameFile against a retained handle rather than by
// path: rotation renames the live WAL out from under its path, and a new file
// that had grown past the cached offset would otherwise be read as if it were
// the tail of the old one.
type walCache struct {
	mu    sync.Mutex
	files map[string]*walCacheFile
	used  int64
}

type walCacheFile struct {
	f       *os.File
	offset  int64 // bytes of f already parsed into entries
	entries []chunk.Entry
	bytes   int64
}

func newWALCache() *walCache {
	return &walCache{files: make(map[string]*walCacheFile)}
}

// forEach calls fn for every entry in the WAL at path, stopping early if fn
// returns false. A missing or unreadable file yields nothing, matching the
// previous behaviour of skipping it.
//
// A WAL that fits the cache is walked from the cached slice. One that does not
// is streamed a record at a time rather than materialized: the budget bounds
// what may be *retained*, and reading a 90MB live hour into a slice just to
// pick 50 rows off it blew that bound on every query — the same "entry count is
// not a memory budget" mistake this cache exists to avoid, one level up. The
// callback shape is what makes the streaming version free: every caller already
// consumes entries one at a time and copies what it keeps.
//
// Cached entries are shared with concurrent scans and streamed ones are
// overwritten by the next record, so in both cases fn must not retain the
// pointer it is handed.
func (c *walCache) forEach(path string, fn func(e *chunk.Entry) bool) {
	if entries, ok := c.cached(path); ok {
		for i := range entries {
			if !fn(&entries[i]) {
				return
			}
		}
		return
	}
	streamWALFile(path, fn)
}

// cached returns the cached parse of path, or ok=false when the file is not
// cacheable within the budget and should be streamed instead.
func (c *walCache) cached(path string) ([]chunk.Entry, bool) {
	if c == nil || WALCacheMaxEntries <= 0 || WALCacheBytes <= 0 {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	cf := c.files[path]
	if cf != nil && !cf.sameFileAs(path) {
		c.removeLocked(path)
		cf = nil
	}
	if cf == nil {
		f, err := os.Open(path)
		if err != nil {
			return nil, false
		}
		cf = &walCacheFile{f: f}
		c.files[path] = cf
		c.evictDeletedLocked(path)
	}

	// Decide against the budget *before* parsing, not after. Checking only the
	// parsed result meant a 90MB hour was still read into one slice on every
	// query and then thrown away for being too big — the allocation the budget
	// exists to prevent, paid in full, with no cache to show for it.
	//
	// On-disk bytes under-count the parsed size (bodies decompress), so this is
	// a floor: anything it admits is still re-checked below.
	if size, err := cf.f.Stat(); err == nil {
		if pending := size.Size() - cf.offset; c.used+pending > WALCacheBytes {
			c.removeLocked(path)
			return nil, false
		}
	}

	// Parse only what was appended since the last read.
	if _, err := cf.f.Seek(cf.offset, io.SeekStart); err != nil {
		c.removeLocked(path)
		return nil, false
	}
	added, consumed, err := wal.ReadEntriesFrom(bufio.NewReaderSize(cf.f, walReadBufferBytes))
	if err != nil {
		slog.Warn("query: WAL had a torn tail; using parsed entries",
			"error", err, "wal", filepath.Base(path), "parsed", len(cf.entries)+len(added))
	}
	cf.offset += consumed
	cf.entries = append(cf.entries, added...)
	addedBytes := entriesMemoryBytes(added)
	cf.bytes += addedBytes
	c.used += addedBytes

	if len(cf.entries) > WALCacheMaxEntries || c.used > WALCacheBytes {
		// Past the budget: drop what was accumulated and let the caller stream.
		// Returning the oversized slice here is what made the budget advisory.
		c.removeLocked(path)
		return nil, false
	}
	return cf.entries, true
}

// streamWALFile walks a WAL a record at a time, reusing one entry. Scanner
// copies every string and body into the destination (see its doc), so the reuse
// is safe as long as the callback does not retain the pointer.
func streamWALFile(path string, fn func(e *chunk.Entry) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := wal.NewScanner(bufio.NewReaderSize(f, walReadBufferBytes))
	var e chunk.Entry
	n := 0
	for sc.Next(&e) {
		n++
		if !fn(&e) {
			return
		}
	}
	if err := sc.Err(); err != nil {
		slog.Warn("query: WAL had a torn tail; using parsed entries",
			"error", err, "wal", filepath.Base(path), "parsed", n)
	}
}

// forget drops the cached parse of path, releasing its entries and handle. The
// store calls it when a WAL is sealed away, so a rotated hour's entries are not
// retained behind a path nothing will query again.
func (c *walCache) forget(path string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.files[path]; ok {
		c.removeLocked(path)
	}
}

// close releases every cached handle.
func (c *walCache) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for path := range c.files {
		c.removeLocked(path)
	}
}

// evictDeletedLocked drops cache entries whose file no longer exists. The cache
// only ever holds the live WAL plus in-flight seals, but a failed seal that is
// eventually cleaned up would otherwise leave its entries pinned forever.
func (c *walCache) evictDeletedLocked(keep string) {
	for path := range c.files {
		if path == keep {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			c.removeLocked(path)
		}
	}
}

// sameFileAs reports whether path still names the file this cache entry holds
// open.
func (cf *walCacheFile) sameFileAs(path string) bool {
	if cf.f == nil {
		return false
	}
	byName, err := os.Stat(path)
	if err != nil {
		return false
	}
	byHandle, err := cf.f.Stat()
	if err != nil {
		return false
	}
	// A truncated (rather than replaced) file has the same identity but can no
	// longer contain the prefix already parsed.
	if byHandle.Size() < cf.offset {
		return false
	}
	return os.SameFile(byName, byHandle)
}

func (cf *walCacheFile) close() {
	if cf.f != nil {
		cf.f.Close()
		cf.f = nil
	}
	cf.entries = nil
}

// removeLocked removes one file and releases its accounted memory. Caller
// holds c.mu.
func (c *walCache) removeLocked(path string) {
	cf, ok := c.files[path]
	if !ok {
		return
	}
	c.used -= cf.bytes
	if c.used < 0 {
		c.used = 0
	}
	cf.close()
	delete(c.files, path)
}

func entriesMemoryBytes(entries []chunk.Entry) int64 {
	var total int64
	for i := range entries {
		total += entryMemoryBytes(&entries[i])
	}
	return total
}

// entryMemoryBytes accounts for the fixed struct plus heap data retained by
// its strings/body. It intentionally slightly over-counts shared string
// backing storage: a conservative budget is safer than pinning the process.
func entryMemoryBytes(e *chunk.Entry) int64 {
	total := int64(unsafe.Sizeof(*e)) + int64(len(e.Body))
	for _, value := range [...]string{
		e.Level, e.Service, e.Message, e.Env, e.Version, e.Host, e.Kind,
		e.EventType, e.TraceID, e.SpanID, e.ParentSpanID, e.RequestID,
		e.UserID, e.TenantID, e.SessionID, e.Method, e.Path, e.Route,
		e.Handler, e.ErrorClass, e.ErrorMessage, e.SourceFile,
		e.ErrorFingerprint, e.JobClass, e.JobQueue, e.JobID,
	} {
		total += int64(len(value))
	}
	return total
}
