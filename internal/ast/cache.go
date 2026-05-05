package ast

import (
	"container/list"
	"sync"
)

// parseCache is a thread-safe LRU cache for parse results.
// It maps file paths to parse results, evicting the least-recently-used
// entry when the cache exceeds maxSize.
type parseCache struct {
	maxSize int
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	order   *list.List
	index   map[string]*list.Element
}

type cacheEntry struct {
	result *ParseResult
	hash   uint64
}

func newParseCache(maxSize int) *parseCache {
	return &parseCache{
		maxSize: maxSize,
		entries: map[string]*cacheEntry{},
		order:   list.New(),
		index:   make(map[string]*list.Element, maxSize),
	}
}

// Get returns the cached ParseResult for path if the stored content hash
// matches hash. Returns nil on cache miss or hash mismatch.
func (c *parseCache) Get(path string, hash uint64) *ParseResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[path]
	if !ok || entry.hash != hash {
		return nil
	}
	// Move to back (most-recently-used) on hit
	if elem, ok := c.index[path]; ok {
		c.order.MoveToBack(elem)
	}
	return entry.result
}

// Set stores res in the cache, evicting the LRU entry if capacity is exceeded.
func (c *parseCache) Set(path string, res *ParseResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.index[path]; ok {
		c.order.MoveToBack(elem)
	} else {
		c.order.PushBack(path)
	}
	c.index[path] = c.order.Back()
	c.entries[path] = &cacheEntry{result: res, hash: res.Hash}

	for len(c.entries) > c.maxSize {
		oldestElem := c.order.Front()
		oldest := oldestElem.Value.(string)
		c.order.Remove(oldestElem)
		delete(c.entries, oldest)
		delete(c.index, oldest)
	}
}

// Clear evicts all entries from the cache.
func (c *parseCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]*cacheEntry{}
	c.order.Init()
	c.index = make(map[string]*list.Element, c.maxSize)
}
