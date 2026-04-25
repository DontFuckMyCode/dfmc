package tools

import (
	"sync"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestEngine_LockPath_SerialisesPerPath pins VULN-025: two
// concurrent acquisitions of the same absolute path serialise so
// the (read-gate-check → write) window is atomic. We measure
// serialisation by recording the entry/exit timestamps of two
// goroutines holding the lock and asserting they don't overlap.
func TestEngine_LockPath_SerialisesPerPath(t *testing.T) {
	e := New(testConfig())
	const path = "/tmp/vuln-025-target.txt"
	const work = 50 * time.Millisecond

	type window struct {
		start, end time.Time
	}
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		got []window
	)
	hold := func() {
		defer wg.Done()
		release := e.LockPath(path)
		defer release()
		start := time.Now()
		time.Sleep(work)
		end := time.Now()
		mu.Lock()
		got = append(got, window{start, end})
		mu.Unlock()
	}
	wg.Add(2)
	go hold()
	go hold()
	wg.Wait()

	if len(got) != 2 {
		t.Fatalf("expected two completed holds, got %d", len(got))
	}
	// Sort by start time so the assertion reads naturally.
	if got[0].start.After(got[1].start) {
		got[0], got[1] = got[1], got[0]
	}
	// Per-path serialisation invariant: the second goroutine must
	// not start before the first finishes. Allow a small clock-
	// resolution slop.
	const slop = 5 * time.Millisecond
	if got[1].start.Before(got[0].end.Add(-slop)) {
		t.Fatalf("two LockPath holds on the same path overlapped: first=%v..%v second=%v..%v",
			got[0].start, got[0].end, got[1].start, got[1].end)
	}
}

// TestEngine_LockPath_AllowsParallelOnDifferentPaths confirms the
// lock granularity is per-path: two goroutines holding DIFFERENT
// absolute paths must run in parallel, not serialised on a single
// engine-wide mutex. This is what makes the fix not regress
// throughput when sub-agents touch unrelated files.
func TestEngine_LockPath_AllowsParallelOnDifferentPaths(t *testing.T) {
	e := New(testConfig())
	const work = 80 * time.Millisecond

	var wg sync.WaitGroup
	start := time.Now()
	hold := func(p string) {
		defer wg.Done()
		release := e.LockPath(p)
		defer release()
		time.Sleep(work)
	}
	wg.Add(2)
	go hold("/tmp/path-A.txt")
	go hold("/tmp/path-B.txt")
	wg.Wait()
	elapsed := time.Since(start)

	// If the two holds had serialised on a global mutex, elapsed
	// would be ~2*work. With per-path locks they run in parallel
	// and elapsed is ~work.
	if elapsed > work+work/2 {
		t.Fatalf("LockPath appears to serialise across paths: elapsed=%v expected ~%v", elapsed, work)
	}
}

// TestEngine_LockPath_ReentrantNoDeadlockDifferentPath confirms a
// single goroutine can hold two different paths without deadlocking
// — this is the apply_patch invariant: it locks per target in
// iteration order, releases between targets. With per-path locks
// (not held re-entrantly) this works as long as the per-iteration
// release happens before the next acquire.
func TestEngine_LockPath_ReleaseBetweenAcquires(t *testing.T) {
	e := New(testConfig())
	r1 := e.LockPath("/tmp/x")
	r1()
	r2 := e.LockPath("/tmp/y")
	r2()
	r3 := e.LockPath("/tmp/x")
	r3()
}

func testConfig() config.Config { return *config.DefaultConfig() }
