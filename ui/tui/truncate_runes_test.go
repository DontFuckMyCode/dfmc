package tui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateForLine_RuneSafe pins that truncateForLine never splits a
// multi-byte rune. It is called all over the TUI on user prompts, Drive task
// text, clarify questions, and coach continuation prompts — content that is
// routinely Turkish (ç ğ ı ş ö ü İ are each 2 bytes in UTF-8). A byte-based
// s[:n] cut lands mid-character ~half the time and emits invalid UTF-8, which
// the terminal renders as a replacement glyph.
func TestTruncateForLine_RuneSafe(t *testing.T) {
	// 10 'ş' runes = 20 bytes. Any odd byte cut splits a rune.
	s := strings.Repeat("ş", 10)
	for n := 1; n < 10; n++ {
		got := truncateForLine(s, n)
		if !utf8.ValidString(got) {
			t.Errorf("truncateForLine(%q, %d) returned invalid UTF-8: %q", s, n, got)
		}
	}
	// Mixed Turkish sentence, narrow widths.
	sentence := "şöyle bir Türkçe cümle yazıyorum buraya çünkü gerekiyor"
	for _, n := range []int{5, 10, 13, 20, 33} {
		got := truncateForLine(sentence, n)
		if !utf8.ValidString(got) {
			t.Errorf("truncateForLine(sentence, %d) returned invalid UTF-8: %q", n, got)
		}
	}
}

// TestTruncateCommandBlock_RuneSafe pins the same rune-safety for the
// command/output block truncator, which clips MAGIC_DOC text, rendered prompt
// bodies, worktree diffs, and command output — any of which may be Turkish.
func TestTruncateCommandBlock_RuneSafe(t *testing.T) {
	body := strings.Repeat("Türkçe ", 50) // multibyte, ~350 bytes
	for _, max := range []int{3, 7, 15, 100, 201} {
		got := truncateCommandBlock(body, max)
		if !utf8.ValidString(got) {
			t.Errorf("truncateCommandBlock(body, %d) returned invalid UTF-8: %q", max, got)
		}
	}
}
