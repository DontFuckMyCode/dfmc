// circuit_breaker_test.go — CircuitBreaker unit tests.

package drive

import (
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedState(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 3})
	if got := cb.State(); got != CircuitClosed {
		t.Fatalf("initial state: got %v, want %v", got, CircuitClosed)
	}
	if !cb.Check() {
		t.Fatal("closed circuit should allow Check")
	}
	cb.Record(true)
	if got := cb.State(); got != CircuitClosed {
		t.Fatalf("after success: got %v, want %v", got, CircuitClosed)
	}
}

func TestCircuitBreaker_TripOnThreshold(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 3})
	cb.Record(false)
	cb.Record(false)
	if got := cb.State(); got != CircuitClosed {
		t.Fatalf("2 failures: got %v, want %v", got, CircuitClosed)
	}
	cb.Record(false)
	if got := cb.State(); got != CircuitOpen {
		t.Fatalf("after 3 failures: got %v, want %v", got, CircuitOpen)
	}
	if cb.Check() {
		t.Fatal("open circuit should reject Check")
	}
}

func TestCircuitBreaker_RecoveryTimeout(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 1,
		RecoveryTimeout:  50 * time.Millisecond,
	})
	cb.Record(false)
	if cb.Check() {
		t.Fatal("should be open right after trip")
	}
	cb.mu.Lock()
	cb.lastFailure = time.Now().Add(-100 * time.Millisecond)
	cb.mu.Unlock()
	if !cb.Check() {
		t.Fatal("should transition to half-open after RecoveryTimeout")
	}
}

func TestCircuitBreaker_SuccessInHalfOpenCloses(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 1,
		RecoveryTimeout:  1 * time.Millisecond,
		HalfOpenMaxCalls: 3,
	})
	cb.Record(false)
	cb.mu.Lock()
	cb.lastFailure = time.Now().Add(-100 * time.Millisecond)
	cb.mu.Unlock()
	if !cb.Check() {
		t.Fatal("should enter half-open and allow first probe")
	}
	cb.Record(true)
	if got := cb.State(); got != CircuitClosed {
		t.Fatalf("probe success should close circuit: got %v, want %v", got, CircuitClosed)
	}
}

func TestCircuitBreaker_FailureInHalfOpenReopens(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 1,
		RecoveryTimeout:  1 * time.Millisecond,
		HalfOpenMaxCalls: 3,
	})
	cb.Record(false)
	cb.mu.Lock()
	cb.lastFailure = time.Now().Add(-100 * time.Millisecond)
	cb.mu.Unlock()
	cb.Check()
	cb.Record(false)
	if got := cb.State(); got != CircuitOpen {
		t.Fatalf("probe failure should reopen: got %v, want %v", got, CircuitOpen)
	}
}

func TestCircuitBreaker_DefaultConfig(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{})
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.cfg.FailureThreshold != 5 {
		t.Fatalf("FailureThreshold: got %d, want 5", cb.cfg.FailureThreshold)
	}
	if cb.cfg.RecoveryTimeout != 2*time.Minute {
		t.Fatalf("RecoveryTimeout: got %v, want 2m", cb.cfg.RecoveryTimeout)
	}
	if cb.cfg.HalfOpenMaxCalls != 3 {
		t.Fatalf("HalfOpenMaxCalls: got %d, want 3", cb.cfg.HalfOpenMaxCalls)
	}
}

func TestCircuitState_String(t *testing.T) {
	tests := []struct {
		s    CircuitState
		want string
	}{
		{CircuitClosed, "closed"},
		{CircuitOpen, "open"},
		{CircuitHalfOpen, "half_open"},
		{CircuitState(99), "unknown"},
	}
	for _, tc := range tests {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("CircuitState(%d).String(): got %q, want %q", tc.s, got, tc.want)
		}
	}
}
