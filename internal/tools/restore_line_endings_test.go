package tools

import (
	"strings"
	"testing"
)

// TestRestoreOriginalLineEndings_InsertPreservesPerLineEnding pins the
// restoreOriginalLineEndings positional mapping bug: when a patch inserts
// lines (net line count increases), updatedLines[i] and endings[i] refer
// to different source-file positions because the insertion shifts the
// anchor. Without offset tracking, inserted lines inherit the dominant
// ending instead of their actual per-line endings.
func TestRestoreOriginalLineEndings_InsertPreservesPerLineEnding(t *testing.T) {
	// Original: mixed-EOL file — line 0 uses LF, line 1 uses CRLF,
	// line 2 uses LF. Dominant ending is CRLF (2/3 lines).
	original := "l0\nl1\r\nl2\n"
	updatedNorm := strings.Join([]string{
		"l0",        // unchanged — original line 0, LF
		"l1",        // unchanged — original line 1, CRLF
		"INSERTED",  // new line — patch inserts this with LF
		"INSERTED2",  // new line — patch inserts this with LF
		"l2",        // unchanged — original line 2, LF
	}, "\n") + "\n"

	got := restoreOriginalLineEndings(original, updatedNorm)

	// The two inserted lines must keep LF, not flip to CRLF.
	// Pre-fix: inserted lines fell through to dominantEnding=\r\n
	// because i >= len(endings) was false but endings[i] pointed
	// at the wrong original line's ending.
	want := "l0\nl1\r\nINSERTED\nINSERTED2\nl2\n"
	if got != want {
		t.Fatalf("inserted lines got wrong EOL:\n  want %q\n  got  %q", want, got)
	}
	// Defensive: unchanged lines kept their original endings.
	if !strings.Contains(got, "l0\n") || !strings.Contains(got, "l1\r\n") {
		t.Fatalf("unchanged lines corrupted: %q", got)
	}
}

// TestRestoreOriginalLineEndings_DeletePreservesPerLineEnding is the
// symmetric pin for deletions: when a patch removes lines (net line
// count decreases), the trailing lines shift backward and their
// endings must still be correct.
func TestRestoreOriginalLineEndings_DeletePreservesPerLineEnding(t *testing.T) {
	// Original: 4 lines, alternating LF/CRLF. Line 2 (CRLF) gets deleted.
	original := "a\nb\r\nc\r\nd\n"
	// After removing "c\r\n", updated has 3 lines: a\n, b\r\n, d\n
	updatedNorm := strings.Join([]string{"a", "b", "d"}, "\n") + "\n"

	got := restoreOriginalLineEndings(original, updatedNorm)

	// d\n was original line 3 (LF) — must stay LF.
	want := "a\nb\r\nd\n"
	if got != want {
		t.Fatalf("after delete, trailing line got wrong EOL:\n  want %q\n  got  %q", want, got)
	}
}
