// Tests for the process-wide cancellation registry. The driver-side
// integration (Run + Resume registering on entry, unregistering on
// exit) is exercised in driver_test.go's cancel/deadline cases; this
// file pins the registry primitives in isolation.

package drive

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistryCancelTriggersFunc(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	register("drv-r1", "task one", cancel)

	if !IsActive("drv-r1") {
		t.Fatal("registered ID should be active")
	}
	if !Cancel("drv-r1") {
		t.Fatal("Cancel must return true on hit")
	}
	if ctx.Err() == nil {
		t.Fatal("ctx must be cancelled by registry Cancel")
	}
	if IsActive("drv-r1") {
		t.Fatal("Cancel must remove the entry so subsequent IsActive returns false")
	}
}

func TestRegistryCancelMissReturnsFalse(t *testing.T) {
	if Cancel("nope-not-here") {
		t.Fatal("Cancel must return false when ID is not registered")
	}
}

func TestRegistryUnregisterRemovesEntry(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	register("drv-u1", "task", cancel)
	unregister("drv-u1")
	if IsActive("drv-u1") {
		t.Fatal("unregister must remove the entry")
	}
	if Cancel("drv-u1") {
		t.Fatal("post-unregister Cancel must return false")
	}
}

func TestRegistryListActiveSnapshot(t *testing.T) {
	// Snapshot count before so concurrent runs (other tests in race
	// mode) don't break the assertion. We only assert "our entries
	// show up" + "go away after unregister", not the absolute count.
	beforeIDs := collectIDs(ListActive())
	_, c1 := context.WithCancel(context.Background())
	_, c2 := context.WithCancel(context.Background())
	register("drv-list-1", "first", c1)
	register("drv-list-2", "second", c2)
	defer unregister("drv-list-1")
	defer unregister("drv-list-2")

	got := collectIDs(ListActive())
	if !contains(got, "drv-list-1") || !contains(got, "drv-list-2") {
		t.Fatalf("ListActive missing newly-registered IDs: before=%v after=%v", beforeIDs, got)
	}
}

func TestRegistryConcurrentSafe(t *testing.T) {
	// Hammer the registry from multiple goroutines to surface any
	// race between register / Cancel / unregister. Without
	// registryMu this would flap under -race.
	const N = 50
	var wg sync.WaitGroup
	cancelled := int64(0)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			runID := "drv-concurrent-" + itoa(id)
			register(runID, "task", cancel)
			// Simulate a brief workload so cancel races with the
			// natural unregister.
			done := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					atomic.AddInt64(&cancelled, 1)
				case <-time.After(20 * time.Millisecond):
				}
				close(done)
			}()
			if id%2 == 0 {
				Cancel(runID)
			}
			<-done
			unregister(runID)
		}(i)
	}
	wg.Wait()
	// We expect roughly N/2 cancellations (the even IDs). Race
	// timing can shave a few off if the goroutine windows up after
	// the timeout, so we accept >= N/3 as evidence the cancel path
	// fired without serializing.
	if atomic.LoadInt64(&cancelled) < int64(N/3) {
		t.Fatalf("expected at least %d cancellations, got %d", N/3, cancelled)
	}
}

func TestRegistryNilCancelIgnored(t *testing.T) {
	// register with nil cancel must be a no-op (defensive).
	register("drv-nil-1", "task", nil)
	if IsActive("drv-nil-1") {
		t.Fatal("nil cancel must not register")
	}
}

func TestRegistryEmptyIDIgnored(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	register("", "task", cancel)
	if IsActive("") {
		t.Fatal("empty ID must not register")
	}
}

// TestDriverIntegrationCancelStopsRun: full integration — start a
// driver run, externally call drive.Cancel(runID) mid-flight, verify
// the run terminates with RunStopped.
func TestDriverIntegrationCancelStopsRun(t *testing.T) {
	runner := &fakeRunner{
		PlanFunc: func(_ PlannerRequest) (string, error) {
			return `{"todos":[
				{"id":"T1","title":"slow","detail":"slow"},
				{"id":"T2","title":"never","detail":"unreached"}
			]}`, nil
		},
	}
	gotRunID := make(chan string, 1)
	cancelled := make(chan struct{})
	runner.ExecFunc = func(req ExecuteTodoRequest) (ExecuteTodoResponse, error) {
		if req.TodoID == "T1" {
			// Discover our own run ID by inspecting the registry —
			// the driver registers before publishing the start event.
			active := ListActive()
			if len(active) > 0 {
				gotRunID <- active[0].RunID
			}
			<-cancelled
			return ExecuteTodoResponse{Summary: "ok"}, nil
		}
		return ExecuteTodoResponse{}, errors.New("T2 should not run after cancel")
	}
	d := NewDriver(runner, nil, nil, Config{})
	done := make(chan *Run, 1)
	go func() {
		run, _ := d.Run(context.Background(), "task")
		done <- run
	}()
	runID := <-gotRunID
	if runID == "" {
		t.Fatal("driver did not register its run ID")
	}
	// External cancel.
	if !Cancel(runID) {
		t.Fatal("Cancel must return true while run is in flight")
	}
	close(cancelled)
	select {
	case run := <-done:
		if run.Status != RunStopped {
			t.Fatalf("expected RunStopped after external Cancel, got %s", run.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("driver did not stop within 2s after external Cancel")
	}
}

// helpers
func collectIDs(active []ActiveRun) []string {
	out := make([]string, 0, len(active))
	for _, a := range active {
		out = append(out, a.RunID)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
