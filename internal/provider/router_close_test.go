package provider

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// closableStub is a stubProvider that also implements ProviderCloser, so we
// can exercise Router.CloseAll's drain + error-join behaviour.
type closableStub struct {
	*stubProvider
	closeErr error
	closed   int32
}

func (c *closableStub) Close() error {
	atomic.AddInt32(&c.closed, 1)
	return c.closeErr
}

// TestRouter_CloseAll_DrainsClosersSkipsOthers verifies CloseAll calls Close
// on every provider implementing ProviderCloser and silently skips those
// that don't (offline/placeholder/stateless providers).
func TestRouter_CloseAll_DrainsClosersSkipsOthers(t *testing.T) {
	a := &closableStub{stubProvider: &stubProvider{name: "a"}}
	b := &closableStub{stubProvider: &stubProvider{name: "b"}}
	plain := &stubProvider{name: "plain"} // no Close method
	r := newRouterWith(a, b, plain)

	if err := r.CloseAll(); err != nil {
		t.Fatalf("CloseAll returned error: %v", err)
	}
	if got := atomic.LoadInt32(&a.closed); got != 1 {
		t.Errorf("provider a Close called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&b.closed); got != 1 {
		t.Errorf("provider b Close called %d times, want 1", got)
	}
	// A second call must not error (the contract is "safe to call again").
	if err := r.CloseAll(); err != nil {
		t.Fatalf("second CloseAll returned error: %v", err)
	}
}

// TestRouter_CloseAll_JoinsErrors verifies a failing closer does not stop the
// others from draining, and that the returned error names the provider and
// wraps the underlying cause.
func TestRouter_CloseAll_JoinsErrors(t *testing.T) {
	good := &closableStub{stubProvider: &stubProvider{name: "good"}}
	bad := &closableStub{stubProvider: &stubProvider{name: "bad"}, closeErr: errors.New("boom")}
	r := newRouterWith(good, bad)

	err := r.CloseAll()
	if err == nil {
		t.Fatal("expected an error from the failing closer")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("joined error should wrap the cause %q, got %v", "boom", err)
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("joined error should name the failing provider, got %v", err)
	}
	if got := atomic.LoadInt32(&good.closed); got != 1 {
		t.Errorf("the healthy provider must still be drained despite the other's error; closed=%d", got)
	}
}

// TestRouter_CloseAll_NoClosersIsNoOp verifies a router whose providers are
// all non-closers returns nil rather than a spurious error.
func TestRouter_CloseAll_NoClosersIsNoOp(t *testing.T) {
	r := newRouterWith(&stubProvider{name: "x"}, &stubProvider{name: "y"})
	if err := r.CloseAll(); err != nil {
		t.Fatalf("CloseAll with no closers should be a no-op, got: %v", err)
	}
}
