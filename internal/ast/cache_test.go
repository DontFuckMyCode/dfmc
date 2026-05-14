package ast

import (
	"testing"
)

// TestParseCacheSetEviction tests the LRU eviction branch (line 73: len > maxSize)
func TestParseCacheSetEviction(t *testing.T) {
	c := newParseCache(2)

	// Fill to maxSize
	p1 := &ParseResult{Hash: 1}
	p2 := &ParseResult{Hash: 2}
	c.Set("/a", p1)
	c.Set("/b", p2) // cache now at maxSize

	// Adding third entry must evict oldest ("/a")
	p3 := &ParseResult{Hash: 3}
	c.Set("/c", p3)

	// "/a" should be gone
	if c.Get("/a", 1) != nil {
		t.Errorf("expected /a to be evicted")
	}
	// "/b" and "/c" should remain
	if c.Get("/b", 2) == nil {
		t.Errorf("expected /b to still be present")
	}
	if c.Get("/c", 3) == nil {
		t.Errorf("expected /c to be present")
	}

	// Verify current size
	if len(c.entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(c.entries))
	}
}

// TestParseCacheUpdateResetsLRU tests that updating an existing key moves it to back
func TestParseCacheUpdateResetsLRU(t *testing.T) {
	c := newParseCache(3)

	c.Set("/a", &ParseResult{Hash: 1})
	c.Set("/b", &ParseResult{Hash: 2})
	c.Set("/c", &ParseResult{Hash: 3})

	// Update "/a" — should move to most-recent
	c.Set("/a", &ParseResult{Hash: 10})

	// Add new entry — should evict "/b" (oldest after "/a" was updated)
	c.Set("/d", &ParseResult{Hash: 4})

	if c.Get("/a", 10) == nil {
		t.Errorf("expected updated /a to be present")
	}
	if c.Get("/b", 2) != nil {
		t.Errorf("expected /b to be evicted after /a update and /d insert")
	}
}

// TestParseCacheGetMissesOnWrongHash tests line 38-39 hash mismatch returns nil
func TestParseCacheGetMissesOnWrongHash(t *testing.T) {
	c := newParseCache(5)
	c.Set("/file", &ParseResult{Hash: 100})

	// Correct hash hits
	if c.Get("/file", 100) == nil {
		t.Error("expected cache hit on correct hash")
	}
	// Wrong hash misses
	if c.Get("/file", 999) != nil {
		t.Error("expected nil on hash mismatch")
	}
}

// TestParseCacheClear tests the Clear() function
func TestParseCacheClear(t *testing.T) {
	c := newParseCache(5)
	c.Set("/a", &ParseResult{Hash: 1})
	c.Set("/b", &ParseResult{Hash: 2})

	c.Clear()

	if len(c.entries) != 0 {
		t.Errorf("expected 0 entries after clear, got %d", len(c.entries))
	}
}
