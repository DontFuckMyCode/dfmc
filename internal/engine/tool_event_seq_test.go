package engine

import (
	"context"
	"testing"
)

// TestAllocToolEventSeq_Monotonic pins the allocator contract:
// each Add must return a strictly larger value than the previous,
// and zero must never be returned (callers use 0 to mean "no seq
// assigned" so the allocator must start at 1).
func TestAllocToolEventSeq_Monotonic(t *testing.T) {
	e := &Engine{}
	seen := make(map[uint64]struct{}, 16)
	prev := uint64(0)
	for i := 0; i < 16; i++ {
		got := e.allocToolEventSeq()
		if got == 0 {
			t.Fatalf("allocToolEventSeq() returned 0 on call %d — reserved sentinel", i)
		}
		if got <= prev {
			t.Fatalf("allocToolEventSeq() not monotonic: prev=%d got=%d", prev, got)
		}
		if _, dup := seen[got]; dup {
			t.Fatalf("allocToolEventSeq() returned duplicate %d", got)
		}
		seen[got] = struct{}{}
		prev = got
	}
}

// TestAllocToolEventSeq_NilSafe — nil Engine returns 0 so call
// sites with optional engine wiring (test fixtures, partially-init
// engines during teardown) don't panic and don't bind a real seq
// to events they shouldn't be tagging.
func TestAllocToolEventSeq_NilSafe(t *testing.T) {
	var e *Engine
	if got := e.allocToolEventSeq(); got != 0 {
		t.Fatalf("nil Engine should return 0, got %d", got)
	}
}

// TestToolEventSeqContext_RoundTrips covers the context-propagation
// contract: withToolEventSeq stores a value retrievable by
// toolEventSeqFromContext; missing key returns 0; nil context
// returns 0. These keep the lifecycle's "stamp Seq from ctx" calls
// safe even when callers forget to wire the value.
func TestToolEventSeqContext_RoundTrips(t *testing.T) {
	t.Run("set then get", func(t *testing.T) {
		ctx := withToolEventSeq(context.Background(), 42)
		if got := toolEventSeqFromContext(ctx); got != 42 {
			t.Fatalf("round-trip failed: got %d, want 42", got)
		}
	})
	t.Run("missing key returns 0", func(t *testing.T) {
		if got := toolEventSeqFromContext(context.Background()); got != 0 {
			t.Fatalf("unset ctx should return 0, got %d", got)
		}
	})
	t.Run("nil ctx returns 0", func(t *testing.T) {
		if got := toolEventSeqFromContext(nil); got != 0 {
			t.Fatalf("nil ctx should return 0, got %d", got)
		}
	})
	t.Run("zero value preserved", func(t *testing.T) {
		// Defensive: callers should NOT pass 0 (it means unset), but
		// the helper must not collapse a deliberately-stored 0 into
		// "unset" via type-assertion failure — if it did, behavior
		// would diverge between "I never set it" and "I set it to 0".
		ctx := withToolEventSeq(context.Background(), 0)
		if got := toolEventSeqFromContext(ctx); got != 0 {
			t.Fatalf("stored 0 should round-trip as 0, got %d", got)
		}
	})
}

// TestAllocToolEventSeq_ConcurrentMonotonic confirms the atomic
// increment is safe under concurrent allocators (the parallel
// dispatcher allocates one seq per goroutine). No duplicates are
// allowed — a duplicate would let two parallel tool calls' events
// collide under one Seq, breaking the dedupe contract.
func TestAllocToolEventSeq_ConcurrentMonotonic(t *testing.T) {
	e := &Engine{}
	const goroutines = 8
	const perGoroutine = 32
	collected := make(chan uint64, goroutines*perGoroutine)
	done := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		go func() {
			for i := 0; i < perGoroutine; i++ {
				collected <- e.allocToolEventSeq()
			}
			done <- struct{}{}
		}()
	}
	for g := 0; g < goroutines; g++ {
		<-done
	}
	close(collected)
	seen := make(map[uint64]struct{}, goroutines*perGoroutine)
	for s := range collected {
		if _, dup := seen[s]; dup {
			t.Fatalf("duplicate Seq under concurrent allocation: %d", s)
		}
		seen[s] = struct{}{}
	}
	if len(seen) != goroutines*perGoroutine {
		t.Fatalf("expected %d distinct seqs, got %d", goroutines*perGoroutine, len(seen))
	}
}
