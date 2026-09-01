package engine

import (
	"container/list"
	"sync"

	"golang.org/x/sync/singleflight"
)

// defaultColumnCacheBytes is the memory budget for decoded chunk columns.
//
// Sealed chunks are immutable, so a decoded column stays valid for as long as
// the file exists — but every query used to decode from zstd again. A single
// entry lookup, and every page of a log search, pays for decoding the whole
// hour's worth of a column no matter how few rows it returns, so the same
// recent hour was decompressed from scratch on every tool call.
//
// 64 MB holds the non-body columns of a few recent hours, which is what
// interactive use touches. It is a ceiling, not a reservation: nothing is
// decoded until a query asks for it.
const defaultColumnCacheBytes = 64 << 20

// columnCache is a byte-bounded LRU over decoded chunk columns, keyed by chunk
// file path and column name.
//
// The cached slices are shared by every reader, so they are strictly read-only.
// Values that leave the engine inside a chunk.Entry are copied out at the row
// level (see chunkRow.sparseBytes and chunkRow.sparseBool) rather than being
// excluded from the cache: a caller writing through such a field would
// otherwise rewrite the chunk's value for every later reader.
type columnCache struct {
	mu     sync.Mutex
	budget int64
	used   int64
	items  map[columnKey]*list.Element
	lru    *list.List // front = most recently used
	loads  singleflight.Group
}

type columnKey struct {
	chunkPath string
	column    string
}

type columnEntry struct {
	key   columnKey
	value any
	bytes int64
}

func newColumnCache(budget int64) *columnCache {
	if budget <= 0 {
		return nil // caching disabled
	}
	return &columnCache{
		budget: budget,
		items:  make(map[columnKey]*list.Element),
		lru:    list.New(),
	}
}

// get returns the cached decoding of a column, if present.
func (c *columnCache) get(key columnKey) (any, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(el)
	return el.Value.(*columnEntry).value, true
}

// maxColumnShareOfBudget caps how much of the budget one column may take.
// Without it a single fat column — a body column of an hour of large payloads —
// evicts every other column in the cache on the way in, and then gets evicted
// itself by the next one, leaving the cache doing pure work for no hits.
const maxColumnShareOfBudget = 4

// put stores a decoded column, evicting least-recently-used entries until the
// budget is met. A column too large for its share of the budget is not cached.
func (c *columnCache) put(key columnKey, value any, size int64) {
	if c == nil || size <= 0 || size > c.budget/maxColumnShareOfBudget {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		c.lru.MoveToFront(el)
		return
	}
	for c.used+size > c.budget {
		oldest := c.lru.Back()
		if oldest == nil {
			return
		}
		e := oldest.Value.(*columnEntry)
		c.lru.Remove(oldest)
		delete(c.items, e.key)
		c.used -= e.bytes
	}
	c.items[key] = c.lru.PushFront(&columnEntry{key: key, value: value, bytes: size})
	c.used += size
}

// stats reports the cache's current occupancy. Used by tests and diagnostics.
func (c *columnCache) stats() (entries int, usedBytes int64) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items), c.used
}

// Size estimates. They only need to be proportional: the budget is a memory
// guard, not an accounting ledger.

const (
	sliceHeaderBytes  = 24 // string / slice header
	pointerBytes      = 8
	int64Bytes        = 8
	perEntryOverhead  = 48 // map entry + list element + columnEntry
	stringSampleLimit = 512
)

func sizeOfInt64s(v []int64) int64 { return int64(len(v))*int64Bytes + perEntryOverhead }

// sizeOfStrings samples the first few values rather than walking a 50k-row
// column: the estimate feeds a memory budget, not a bill.
func sizeOfStrings(v []string) int64 {
	total := int64(len(v)) * sliceHeaderBytes
	n := min(len(v), stringSampleLimit)
	if n > 0 {
		var sampled int64
		for _, s := range v[:n] {
			sampled += int64(len(s))
		}
		total += sampled / int64(n) * int64(len(v))
	}
	return total + perEntryOverhead
}

func sizeOfOptInt64s(v []*int64) int64 {
	total := int64(len(v)) * pointerBytes
	for _, p := range v {
		if p != nil {
			total += int64Bytes
		}
	}
	return total + perEntryOverhead
}

func sizeOfBlobs(v [][]byte) int64 {
	total := int64(len(v)) * sliceHeaderBytes
	for _, b := range v {
		total += int64(len(b))
	}
	return total + perEntryOverhead
}

func sizeOfBools(v []*bool) int64 {
	total := int64(len(v)) * pointerBytes
	for _, p := range v {
		if p != nil {
			total++
		}
	}
	return total + perEntryOverhead
}

// ColumnCacheBytes is the budget used by every new Store. It is a variable so a
// memory-constrained deployment can lower it (or set it to 0 to disable the
// cache) before the store is opened.
var ColumnCacheBytes int64 = defaultColumnCacheBytes
