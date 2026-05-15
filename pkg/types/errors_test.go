package types

import (
	"testing"
	"time"
)

func TestSafeGoRecoversPanic(t *testing.T) {
	done := make(chan struct{})
	SafeGo("panic-test", func() {
		defer close(done)
		panic("boom")
	})

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("safe goroutine did not complete")
	}
}

// SafeGoPanicObserver nil behavior

func TestSafeGoNoObserverInstalled(t *testing.T) {
	SetSafeGoPanicObserver(nil)

	done := make(chan struct{})
	SafeGo("no-observer-test", func() {
		defer close(done)
		panic("boom without observer")
	})

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("SafeGo with nil observer did not complete")
	}
}

func TestSafeGoObserverCleared(t *testing.T) {
	SetSafeGoPanicObserver(nil)

	done := make(chan struct{})
	SafeGo("cleared-observer-test", func() {
		defer close(done)
		panic("boom after clear")
	})

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("SafeGo after SetSafeGoPanicObserver(nil) did not complete")
	}
}

func TestSafeGoSecondaryPanicInObserver(t *testing.T) {
	// Clear first so previous test's goroutines can't interfere.
	SetSafeGoPanicObserver(nil)
	SetSafeGoPanicObserver(func(name string, recovered any, stack []byte) {
		panic("secondary panic in observer")
	})
	t.Cleanup(func() { SetSafeGoPanicObserver(nil) })

	done := make(chan struct{})
	SafeGo("secondary-panic-test", func() {
		defer close(done)
		panic("primary panic")
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SafeGo did not complete after secondary panic in observer")
	}
}

// Panic in observer guard

func TestSafeGoPanicInObserverDoesNotPropagate(t *testing.T) {
	SetSafeGoPanicObserver(func(name string, recovered any, stack []byte) {
		panic("observer panic that must not propagate")
	})
	t.Cleanup(func() { SetSafeGoPanicObserver(nil) })

	done := make(chan struct{})
	SafeGo("observer-panic-guard-test", func() {
		defer close(done)
		panic("initial panic")
	})

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("SafeGo did not complete when observer panicked")
	}

	// Verify the deferred recover in observer guard caught the secondary panic
	// by checking that we can still call SafeGo again (the process is not broken)
	done2 := make(chan struct{})
	SafeGo("post-observer-panic-test", func() {
		defer close(done2)
		// No panic this time
	})

	select {
	case <-done2:
	case <-time.After(1 * time.Second):
		t.Fatal("SafeGo did not complete after observer panic")
	}
}
