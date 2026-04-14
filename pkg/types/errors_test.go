package types

import (
	"errors"
	"testing"
	"time"
)

func TestErrorHelpers(t *testing.T) {
	rateErr := &DFMCError{Kind: ErrRateLimit, Message: "rate limited"}
	if !IsRateLimit(rateErr) {
		t.Fatal("expected IsRateLimit true")
	}
	if IsTimeout(rateErr) {
		t.Fatal("expected IsTimeout false")
	}

	timeoutErr := &DFMCError{Kind: ErrTimeout, Message: "timeout"}
	if !IsTimeout(timeoutErr) {
		t.Fatal("expected IsTimeout true")
	}

	wrapped := &DFMCError{Kind: ErrConfig, Message: "bad config", Cause: errors.New("detail")}
	if !IsConfig(wrapped) {
		t.Fatal("expected IsConfig true")
	}
}

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
