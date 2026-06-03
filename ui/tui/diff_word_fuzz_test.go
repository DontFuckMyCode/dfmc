package tui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzComputeWordDiff pins the intra-line diff splitter. It walks two strings
// byte-by-byte to find a common prefix/suffix, then clamps both back to rune
// boundaries before slicing into (prefix, middle, suffix). This is exactly the
// stateful byte-offset class that has hidden real panics elsewhere, and it
// feeds it diff-line content that routinely contains multibyte (Turkish) code.
//
// Invariants for any (left, right):
//   - never panics
//   - prefix+middle+suffix reconstructs each side byte-for-byte (no lost or
//     duplicated bytes from the prefix/suffix clamping)
//   - both sides share the SAME prefix and SAME suffix (the common parts)
//   - if an input is valid UTF-8, none of its three parts split a rune
func FuzzComputeWordDiff(f *testing.F) {
	seeds := [][2]string{
		{"func Foo(x int)", "func Bar(x int)"},
		{"+ x", "+ y"},
		{"same", "same"},
		{"", ""},
		{"", "added"},
		{"removed", ""},
		{"İstanbul köy", "İzmir köy"}, // Turkish, shared multibyte prefix/suffix
		{"a日本b", "a中文b"},              // CJK middle, shared ASCII ends
		{"🚀ship it", "🚀shipped"},      // emoji prefix
		{"prefİx", "prefiy"},          // multibyte right at the boundary
		{strings.Repeat("x", 200), strings.Repeat("x", 199) + "y"},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1])
	}

	f.Fuzz(func(t *testing.T, left, right string) {
		lp, rp := computeWordDiff(left, right) // must never panic

		if got := lp.prefix + lp.middle + lp.suffix; got != left {
			t.Fatalf("left reconstruction lost bytes: in=%q out=%q (parts %q|%q|%q)", left, got, lp.prefix, lp.middle, lp.suffix)
		}
		if got := rp.prefix + rp.middle + rp.suffix; got != right {
			t.Fatalf("right reconstruction lost bytes: in=%q out=%q (parts %q|%q|%q)", right, got, rp.prefix, rp.middle, rp.suffix)
		}
		// The common prefix/suffix must be identical on both sides.
		if lp.prefix != rp.prefix {
			t.Fatalf("prefixes differ: %q vs %q (left=%q right=%q)", lp.prefix, rp.prefix, left, right)
		}
		if lp.suffix != rp.suffix {
			t.Fatalf("suffixes differ: %q vs %q (left=%q right=%q)", lp.suffix, rp.suffix, left, right)
		}
		// Rune integrity: a valid-UTF-8 input must yield valid-UTF-8 parts.
		for _, side := range []struct {
			in string
			p  wordDiffParts
		}{{left, lp}, {right, rp}} {
			if !utf8.ValidString(side.in) {
				continue
			}
			if !utf8.ValidString(side.p.prefix) || !utf8.ValidString(side.p.middle) || !utf8.ValidString(side.p.suffix) {
				t.Fatalf("valid input %q split into invalid-UTF-8 parts %q|%q|%q", side.in, side.p.prefix, side.p.middle, side.p.suffix)
			}
		}
	})
}
