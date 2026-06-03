package tools

import (
	"strings"
	"testing"
)

// FuzzSplitLinesEndingsRoundTrip pins that splitLinesAndEndings is lossless:
// re-joining each line with its captured ending must reproduce the input
// byte-for-byte. edit_file relies on this to restore a file's original CRLF/LF
// layout after a normalized edit — any byte the split drops or invents would
// corrupt the written file.
func FuzzSplitLinesEndingsRoundTrip(f *testing.F) {
	for _, s := range []string{"", "a", "a\n", "a\nb", "a\r\nb\r\n", "a\r\nb\n", "\n", "\r\n", "x\n\ny", "no-eol", "\r", "a\rb"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		lines, endings := splitLinesAndEndings(s)
		if len(lines) != len(endings) {
			t.Fatalf("len mismatch: %d lines vs %d endings", len(lines), len(endings))
		}
		var b strings.Builder
		for i := range lines {
			b.WriteString(lines[i])
			b.WriteString(endings[i])
		}
		if got := b.String(); got != s {
			t.Fatalf("round-trip lost bytes:\n in=%q\nout=%q", s, got)
		}
	})
}

// FuzzRestoreLineEndingsPreservesContent pins that restoring a file's line
// endings never alters the edit's CONTENT — only the \n vs \r\n choice. The
// edited text (updatedNorm, already LF-normalized) must survive intact: with
// every \r\n collapsed back to \n, the result equals updatedNorm. If the
// restoration ever drops, duplicates, or mangles a line, edit_file would write
// a corrupted file — a silent data-loss bug far worse than a panic. It must
// also never panic for any (original, updatedNorm) pair.
func FuzzRestoreLineEndingsPreservesContent(f *testing.F) {
	seeds := []struct{ orig, upd string }{
		{"a\nb\nc\n", "a\nB\nc\n"},
		{"a\r\nb\r\n", "a\r\nb\r\n"},
		{"x\ny\n", "x\ny\nz\n"},
		{"x\ny\nz\n", "x\nz\n"},
		{"", "new\n"},
		{"only", "only-edited"},
		{"a\r\nb\nc", "a\nb\nc\n"},
	}
	for _, s := range seeds {
		f.Add(s.orig, s.upd)
	}
	f.Fuzz(func(t *testing.T, orig, upd string) {
		// updatedNorm's contract is LF-only (it comes from ReplaceAll(src,
		// "\r\n", "\n") in Execute). Strip every CR so the input genuinely
		// honours that contract — a single ReplaceAll("\r\n","\n") does NOT,
		// because it leaves a residual "\r\n" on inputs like "\r\r\n", which
		// would make the collapse-comparison below ill-defined.
		upd = strings.ReplaceAll(upd, "\r", "")

		got := restoreOriginalLineEndings(orig, upd)

		// The restored output, with CRLF collapsed back to LF, must equal the
		// edit content (allowing the function's trailing-newline normalization).
		// Content (everything but the \n-vs-\r\n choice) must never change.
		gotNorm := strings.ReplaceAll(got, "\r\n", "\n")
		if gotNorm != upd && gotNorm != strings.TrimSuffix(upd, "\n") && gotNorm+"\n" != upd {
			t.Fatalf("restore altered content:\n orig=%q\n upd=%q\n got=%q\n gotNorm=%q", orig, upd, got, gotNorm)
		}
	})
}
