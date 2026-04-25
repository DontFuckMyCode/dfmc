// Tests for the bounded buffer used to cap hook stdout/stderr.
// These run pure Go — no subprocess — so they exercise the cap
// edges deterministically.

package hooks

import (
	"strings"
	"testing"
)

func TestBoundedBuffer_AcceptsUpToCap(t *testing.T) {
	b := newBoundedBuffer(10)
	n, err := b.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("unexpected first write: n=%d err=%v", n, err)
	}
	n, err = b.Write([]byte("world"))
	if err != nil || n != 5 {
		t.Fatalf("unexpected second write: n=%d err=%v", n, err)
	}
	if got := b.String(); got != "helloworld" {
		t.Fatalf("want %q, got %q", "helloworld", got)
	}
}

// Crossing the cap mid-write must keep the prefix that fits and drop
// the rest, marking the result as truncated. The Writer must report
// n=len(p) so the source process keeps moving.
func TestBoundedBuffer_TruncatesAtCap(t *testing.T) {
	b := newBoundedBuffer(5)
	n, err := b.Write([]byte("hello-world")) // 11 bytes
	if err != nil {
		t.Fatalf("write err: %v", err)
	}
	if n != 11 {
		t.Fatalf("Writer must report len(p) so the producer doesn't block: got %d", n)
	}
	got := b.String()
	if !strings.HasPrefix(got, "hello") {
		t.Fatalf("missing kept prefix: %q", got)
	}
	if !strings.Contains(got, "[hook output truncated") {
		t.Fatalf("missing truncation marker: %q", got)
	}
	if strings.Contains(got, "-world") {
		t.Fatalf("dropped tail leaked into output: %q", got)
	}
}

// Writes after the cap is hit must be silently consumed (n reported
// equal to len(p)) so the child process doesn't block on a full pipe.
// The kept content must not grow.
func TestBoundedBuffer_DiscardsAfterCap(t *testing.T) {
	b := newBoundedBuffer(3)
	if _, err := b.Write([]byte("abcdef")); err != nil {
		t.Fatalf("first: %v", err)
	}
	beforeLen := len(b.String())
	n, err := b.Write([]byte("ghijkl"))
	if err != nil || n != 6 {
		t.Fatalf("post-cap write should claim full length: n=%d err=%v", n, err)
	}
	if got := b.String(); len(got) != beforeLen {
		t.Fatalf("post-cap write grew the buffer: was %d, now %d (%q)", beforeLen, len(got), got)
	}
}

// A zero or negative cap is clamped to 1 — calling code shouldn't
// have to defensively check before constructing.
func TestBoundedBuffer_ZeroCapClampsToOne(t *testing.T) {
	b := newBoundedBuffer(0)
	if _, err := b.Write([]byte("xy")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := b.String()
	if !strings.HasPrefix(got, "x") || !strings.Contains(got, "truncated") {
		t.Fatalf("zero cap should have kept 1 byte and marked truncation, got: %q", got)
	}
}

// Exact-fit writes must NOT report truncation — the cap is a
// "discard beyond" semantic, not "discard at or beyond."
func TestBoundedBuffer_ExactFitNotTruncated(t *testing.T) {
	b := newBoundedBuffer(5)
	if _, err := b.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := b.String(); got != "hello" {
		t.Fatalf("exact-fit should not be marked truncated: %q", got)
	}
}

// Truncated reports true after writes exceed cap.
func TestBoundedBuffer_Truncated(t *testing.T) {
	b := newBoundedBuffer(5)
	if b.Truncated() {
		t.Error("new buffer should not be truncated")
	}
	_, _ = b.Write([]byte("hello")) // exactly at cap
	if b.Truncated() {
		t.Error("exact-fit should not be truncated")
	}
	_, _ = b.Write([]byte("x")) // exceeds cap
	if !b.Truncated() {
		t.Error("should be truncated after exceeding cap")
	}
}

// Truncated is false when cap is not exceeded.
func TestBoundedBuffer_Truncated_False(t *testing.T) {
	b := newBoundedBuffer(100)
	_, _ = b.Write([]byte("short"))
	if b.Truncated() {
		t.Error("short write should not be truncated")
	}
}
