// Tests for runWithPanicGuard — the testable core of TUI panic
// recovery. Without the guard, a panic inside a bubbletea panel
// leaves the terminal stuck in alt-screen + mouse-capture + hidden-
// cursor mode and the user is staring at a blank screen.

package tui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRunWithPanicGuard_HappyPathReturnsErrUnchanged(t *testing.T) {
	var out bytes.Buffer
	want := errors.New("clean exit")
	got := runWithPanicGuard(&out, func() error {
		return want
	})
	if got != want {
		t.Fatalf("want %v, got %v", want, got)
	}
	if out.Len() != 0 {
		t.Fatalf("no panic → no ANSI reset should be written, got: %q", out.String())
	}
}

func TestRunWithPanicGuard_NilErrorStaysNil(t *testing.T) {
	var out bytes.Buffer
	if err := runWithPanicGuard(&out, func() error { return nil }); err != nil {
		t.Fatalf("nil return should not be rewritten, got %v", err)
	}
}

// The core guarantee: on panic, we emit the ANSI reset sequences AND
// return a wrapped error. Without either half, the caller can't
// distinguish a crash from a clean exit and can't restore the screen.
func TestRunWithPanicGuard_RecoversAndResetsTerminal(t *testing.T) {
	var out bytes.Buffer
	err := runWithPanicGuard(&out, func() error {
		panic("boom")
	})
	if err == nil {
		t.Fatal("panic should surface as a non-nil error")
	}
	if !strings.Contains(err.Error(), "tui panic") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should name panic cause, got: %v", err)
	}

	// Required ANSI resets. Missing any of these means some terminal
	// state can survive a crash and leave the user stuck.
	stderr := out.String()
	required := []string{
		"\x1b[?1049l", // exit alt screen
		"\x1b[?1000l", // disable mouse reporting (X10)
		"\x1b[?1002l", // disable button-event tracking
		"\x1b[?1006l", // disable SGR mouse mode
		"\x1b[?25h",   // show cursor
	}
	for _, seq := range required {
		if !strings.Contains(stderr, seq) {
			t.Errorf("missing ANSI reset %q in:\n%q", seq, stderr)
		}
	}
	// The panic value and a stack trace should make it into the output
	// so the user can file a useful bug report.
	if !strings.Contains(stderr, "DFMC TUI crashed") {
		t.Error("output missing `DFMC TUI crashed` banner")
	}
	if !strings.Contains(stderr, "panic_guard_test.go") {
		// The stack trace should mention this test file since that's
		// where the panic originated. If the guard ever drops the
		// stack trace, users lose debuggability.
		t.Error("output missing stack trace pointing at caller")
	}
}

// A non-string panic value must still be recovered (not rethrown).
// The error message just carries the %v rendering — we don't try to
// be clever about formatting struct panics.
func TestRunWithPanicGuard_RecoversNonStringPanic(t *testing.T) {
	var out bytes.Buffer
	err := runWithPanicGuard(&out, func() error {
		panic(errors.New("wrapped"))
	})
	if err == nil || !strings.Contains(err.Error(), "wrapped") {
		t.Fatalf("non-string panic not surfaced cleanly, got: %v", err)
	}
}
