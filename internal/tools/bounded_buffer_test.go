// Tests for the local boundedBuffer used by run_command. Mirrors the
// shape of internal/hooks/bounded_buffer_test.go but pinned here so
// the two stay independently maintainable.

package tools

import (
	"strings"
	"testing"
)

func TestToolsBoundedBuffer_AcceptsUpToCap(t *testing.T) {
	b := newBoundedBuffer(10)
	if n, err := b.Write([]byte("hello")); err != nil || n != 5 {
		t.Fatalf("first write: n=%d err=%v", n, err)
	}
	if n, err := b.Write([]byte("world")); err != nil || n != 5 {
		t.Fatalf("second write: n=%d err=%v", n, err)
	}
	if got := b.String(); got != "helloworld" {
		t.Fatalf("want %q, got %q", "helloworld", got)
	}
	if b.Len() != 10 {
		t.Fatalf("Len() = %d, want 10", b.Len())
	}
}

// Cap crossing keeps the prefix, drops the rest, marks truncated.
// Producer must still see n=len(p) so the child doesn't block.
func TestToolsBoundedBuffer_TruncatesAtCap(t *testing.T) {
	b := newBoundedBuffer(5)
	n, err := b.Write([]byte("hello-world"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != 11 {
		t.Fatalf("Writer must report len(p) so producer keeps moving: got %d", n)
	}
	got := b.String()
	if !strings.HasPrefix(got, "hello") {
		t.Fatalf("missing kept prefix: %q", got)
	}
	if !strings.Contains(got, "[output truncated") {
		t.Fatalf("missing truncation marker: %q", got)
	}
	if strings.Contains(got, "-world") {
		t.Fatalf("dropped tail leaked: %q", got)
	}
}

func TestToolsBoundedBuffer_DiscardsAfterCap(t *testing.T) {
	b := newBoundedBuffer(3)
	if _, err := b.Write([]byte("abcdef")); err != nil {
		t.Fatalf("first: %v", err)
	}
	beforeLen := b.Len()
	n, err := b.Write([]byte("ghijkl"))
	if err != nil || n != 6 {
		t.Fatalf("post-cap write should claim full length: n=%d err=%v", n, err)
	}
	if b.Len() != beforeLen {
		t.Fatalf("post-cap write grew the buffer: was %d, now %d", beforeLen, b.Len())
	}
}

func TestToolsBoundedBuffer_ZeroCapClampsToOne(t *testing.T) {
	b := newBoundedBuffer(0)
	if _, err := b.Write([]byte("xy")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := b.String(); !strings.HasPrefix(got, "x") || !strings.Contains(got, "truncated") {
		t.Fatalf("zero cap should keep 1 byte and mark truncated, got: %q", got)
	}
}

func TestToolsBoundedBuffer_ExactFitNotTruncated(t *testing.T) {
	b := newBoundedBuffer(5)
	if _, err := b.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := b.String(); got != "hello" {
		t.Fatalf("exact fit should not truncate: %q", got)
	}
}
