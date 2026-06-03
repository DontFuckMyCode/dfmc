package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestAttrValueMatches_ToLowerAliasing pins that attrValueMatches no longer
// derives a byte offset from the lowercased line and applies it to the
// original line. strings.ToLower changes byte length for some runes, so that
// aliasing both misaligned the extracted value (wrong/missed match on Turkish
// 'İ') and could slice past the end of line and panic (growing rune 'Ⱥ').
func TestAttrValueMatches_ToLowerAliasing(t *testing.T) {
	t.Run("no panic on byte-growing rune", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("attrValueMatches panicked on growing rune: %v", r)
			}
		}()
		// 'Ⱥ' U+023A -> 'ⱥ' U+2C65 grows 2 bytes to 3, so the lowercased line
		// is LONGER than the original; the old code overshot len(line).
		_ = attrValueMatches("Ⱥid=", "id", "x", "")
	})

	t.Run("Turkish leading rune still matches", func(t *testing.T) {
		// 'İ' before the attr shrinks under ToLower; the correct id value is
		// "x", which matches name "x". The aliasing bug returned false.
		if !attrValueMatches("İ id=\"x\"", "id", "x", "") {
			t.Error("attrValueMatches with leading 'İ' = false, want true")
		}
		// Class form with a Turkish char before the matched class token.
		if !attrValueMatches("<p class=\"İç hedef\" id=\"a\">", "class", "hedef", "") {
			t.Error("class match after Turkish token = false, want true")
		}
	})

	t.Run("ASCII behaviour unchanged", func(t *testing.T) {
		if !attrValueMatches(`<div id="main">`, "id", "main", "") {
			t.Error("plain ASCII id match regressed")
		}
		if attrValueMatches(`<div id="main">`, "id", "other", "") {
			t.Error("non-matching id wrongly matched")
		}
		if !attrValueMatches(`<a class="btn primary">`, "class", "primary", "") {
			t.Error("multi-class match regressed")
		}
	})
}

// TestOrigByteOffset pins the low->line offset mapping used by the fix.
func TestOrigByteOffset(t *testing.T) {
	cases := []string{"plain ascii", "İ leading", "Ⱥ growing", "çğış mixed", ""}
	for _, line := range cases {
		low := strings.ToLower(line)
		// Every valid low offset must map to a real line byte offset that
		// starts a rune (or len(line)), and never exceed len(line).
		for off := 0; off <= len(low); off++ {
			got := origByteOffset(line, low, off)
			if got < 0 || got > len(line) {
				t.Fatalf("origByteOffset(%q,%d)=%d out of [0,%d]", line, off, got, len(line))
			}
			if got < len(line) && !utf8.RuneStart(line[got]) {
				t.Fatalf("origByteOffset(%q,%d)=%d lands mid-rune", line, off, got)
			}
		}
	}
}
