package index

import (
	"container/list"
	"os"
	"sync"

	"golang.org/x/sync/singleflight"
)

// Cache is a byte-bounded LRU of parsed immutable inverted indexes. Opening an
// index used to ReadFile and rebuild every term string on each full-text query.
type Cache struct {
	mu     sync.Mutex
	budget int64
	used   int64
	items  map[string]*list.Element
	lru    *list.List
	loads  singleflight.Group
}

type cacheEntry struct {
	path   string
	reader *Reader
	bytes  int64
}

func NewCache(budget int64) *Cache {
	if budget <= 0 {
		return nil
	}
	return &Cache{budget: budget, items: make(map[string]*list.Element), lru: list.New()}
}

func (c *Cache) Open(path string) (*Reader, error) {
	if c == nil {
		return OpenReader(path)
	}
	if reader, ok := c.get(path); ok {
		return reader, nil
	}
	value, err, _ := c.loads.Do(path, func() (any, error) {
		if reader, ok := c.get(path); ok {
			return reader, nil
		}
		reader, openErr := OpenReader(path)
		if openErr != nil {
			return nil, openErr
		}
		size := int64(0)
		if info, statErr := os.Stat(path); statErr == nil {
			size = info.Size()
		}
		for _, term := range reader.terms {
			size += int64(len(term.term)) + 24
		}
		c.put(path, reader, size)
		return reader, nil
	})
	if err != nil {
		return nil, err
	}
	return value.(*Reader), nil
}

func (c *Cache) get(path string) (*Reader, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[path]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(el)
	return el.Value.(*cacheEntry).reader, true
}

func (c *Cache) put(path string, reader *Reader, size int64) {
	if size <= 0 || size > c.budget/2 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[path]; ok {
		c.lru.MoveToFront(el)
		return
	}
	for c.used+size > c.budget {
		oldest := c.lru.Back()
		if oldest == nil {
			return
		}
		entry := oldest.Value.(*cacheEntry)
		delete(c.items, entry.path)
		c.lru.Remove(oldest)
		c.used -= entry.bytes
	}
	c.items[path] = c.lru.PushFront(&cacheEntry{path: path, reader: reader, bytes: size})
	c.used += size
}

func (c *Cache) Stats() (entries int, usedBytes int64) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items), c.used
}
