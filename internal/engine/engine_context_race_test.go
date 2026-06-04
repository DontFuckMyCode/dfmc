package engine

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestContextBuildOptions_FileMapsAreClonedNotAliased is the regression for
// the data race where contextBuildOptions read (and aliased) the engine-owned
// modifiedFiles/seenFiles maps without holding e.mu, while file tools write
// them under e.mu — a `fatal error: concurrent map read and map write`
// reachable from the web/TUI context-budget handlers during an active Ask.
//
// The deterministic half asserts clone semantics: the returned opts maps must
// be independent copies (mutating the engine maps afterwards, or deleting from
// the opts maps as BuildWithOptions does, must not cross-contaminate). The
// concurrent half runs reads against writes so `go test -race` (CI) flags any
// regression to unsynchronized access.
func TestContextBuildOptions_FileMapsAreClonedNotAliased(t *testing.T) {
	eng, _, _ := buildGuardTestEngine(t, 0, 1, nil)
	eng.modifiedFiles = map[string]time.Time{"a.go": time.Now()}
	eng.seenFiles = map[string]struct{}{"b.go": {}}

	// Clone semantics: deleting from the returned maps (BuildWithOptions
	// deletes stale entries) must not touch the engine's maps, and a later
	// engine write must not change an already-returned opts.
	opts := eng.contextBuildOptions("explain a")
	for k := range opts.ExcludeStaleFilters {
		delete(opts.ExcludeStaleFilters, k)
	}
	eng.mu.RLock()
	engCount := len(eng.modifiedFiles)
	eng.mu.RUnlock()
	if engCount == 0 {
		t.Fatal("deleting from opts.ExcludeStaleFilters mutated the engine map — not a clone")
	}

	opts2 := eng.contextBuildOptions("explain a")
	before := len(opts2.ExcludeStaleFilters)
	eng.mu.Lock()
	eng.modifiedFiles["added-after.go"] = time.Now()
	eng.mu.Unlock()
	if len(opts2.ExcludeStaleFilters) != before {
		t.Fatal("a post-call engine write changed a prior opts.ExcludeStaleFilters — not a clone")
	}

	// Concurrent reads vs writes (race detector catches unsynchronized access).
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			eng.mu.Lock()
			eng.modifiedFiles[fmt.Sprintf("f%d.go", i)] = time.Now()
			eng.seenFiles[fmt.Sprintf("s%d.go", i)] = struct{}{}
			eng.mu.Unlock()
		}
	}()
	for i := 0; i < 300; i++ {
		o := eng.contextBuildOptions("explain a")
		for k := range o.ExcludeStaleFilters {
			delete(o.ExcludeStaleFilters, k)
		}
	}
	close(stop)
	wg.Wait()
}
