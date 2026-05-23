package tui

// diff_word.go — intra-line "what changed inside this row" highlighter
// for the Patch panel's side-by-side view.
//
// When a removed line pairs with an added line, the whole-line red /
// green colouring tells the user "something here is different" but
// hides WHAT changed. Renaming a single identifier inside a 120-char
// function signature shows up as the entire line being red on the
// left, the entire line green on the right — the eye has to chase
// every character to find the rename.
//
// This pass spots the differing middle by:
//   1. Finding the longest common prefix of the two strings.
//   2. Finding the longest common suffix (must not overlap the
//      prefix — otherwise we'd double-count common chars on tiny
//      edits like "+ x" vs "+ y").
//   3. Splitting each side into (prefix · middle · suffix).
//
// Prefix + suffix render in the same diffRemove/diffAdd foreground
// as before, while the middle gets an extra bold + bg-highlight so
// the eye lands directly on it. When no characters are common the
// algorithm degrades to "whole line is the middle" which matches
// the prior behaviour.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

// wordDiffParts holds the three slices produced by splitting a string
// along its common prefix/suffix with a peer line. middle may be empty
// when the side is wholly contained in the peer (e.g. additions of
// trailing text — the "removed" side has no middle, the "added" side
// has the new tail as middle).
type wordDiffParts struct {
	prefix string
	middle string
	suffix string
}

// computeWordDiff returns the (left, right) split for a paired diff row.
// Identical strings produce empty middles — the caller can skip
// highlighting in that case. The split operates on bytes since the
// callers feed it line content that has already been validated as
// well-formed UTF-8 by the unified-diff parser.
func computeWordDiff(left, right string) (wordDiffParts, wordDiffParts) {
	if left == right {
		return wordDiffParts{prefix: left}, wordDiffParts{prefix: right}
	}
	// Common prefix walks both sides byte-by-byte. For multibyte runes
	// the walker stops at the byte that first differs, which is always
	// at a rune boundary because UTF-8 self-synchronises — a leading
	// byte (< 0x80 or >= 0xC0) cannot follow a continuation byte across
	// a "shared" / "different" boundary.
	pre := 0
	for pre < len(left) && pre < len(right) && left[pre] == right[pre] {
		pre++
	}
	// Common suffix walks back from each end. We clamp so the suffix
	// cannot overlap the prefix on either side — small edits like
	// `+ x` vs `+ y` would otherwise pair-match every char twice.
	suf := 0
	maxSuf := minInt(len(left)-pre, len(right)-pre)
	for suf < maxSuf && left[len(left)-1-suf] == right[len(right)-1-suf] {
		suf++
	}
	// Step pre and suf back to safe rune boundaries so we don't split a
	// multibyte sequence in the middle.
	pre = clampRuneBoundary(left, pre, false)
	pre = clampRuneBoundary(right, pre, false)
	suf = clampRuneBoundary(left, suf, true)
	suf = clampRuneBoundary(right, suf, true)

	lp := wordDiffParts{
		prefix: left[:pre],
		middle: left[pre : len(left)-suf],
		suffix: left[len(left)-suf:],
	}
	rp := wordDiffParts{
		prefix: right[:pre],
		middle: right[pre : len(right)-suf],
		suffix: right[len(right)-suf:],
	}
	return lp, rp
}

// clampRuneBoundary backs up `n` until it lands at a rune boundary in
// `s`. `fromEnd=true` means n is counted from the END of s (suffix
// length). Returns the adjusted count. UTF-8 continuation bytes have
// the high bits 10xxxxxx; we step back until we don't see one at the
// split position.
func clampRuneBoundary(s string, n int, fromEnd bool) int {
	for n > 0 && n < len(s) {
		idx := n
		if fromEnd {
			idx = len(s) - n
		}
		b := s[idx]
		if b&0xC0 != 0x80 {
			return n
		}
		n--
	}
	return n
}

// applyWordDiffStyling renders one side of a paired remove/add row with
// the changed middle highlighted. `base` is the colour the cell would
// have had without the word diff (FailStyle for remove, OkStyle for
// add); the middle gets the same foreground PLUS bold + a dim
// background so the eye anchors on it. When parts.middle is empty
// the function just returns the base-styled prefix — the cell is
// purely common with its peer, which only happens on no-op rows
// (filtered out by the caller before we get here).
func applyWordDiffStyling(parts wordDiffParts, base lipgloss.Style, accentBg lipgloss.Color) string {
	if parts.middle == "" {
		return base.Render(parts.prefix + parts.suffix)
	}
	highlight := base.Bold(true).Background(accentBg)
	var b strings.Builder
	if parts.prefix != "" {
		b.WriteString(base.Render(parts.prefix))
	}
	b.WriteString(highlight.Render(parts.middle))
	if parts.suffix != "" {
		b.WriteString(base.Render(parts.suffix))
	}
	return b.String()
}

// wordDiffBgRemove / wordDiffBgAdd source their colour from the theme
// palette so the "no hex literals outside theme/" invariant holds.
// Kept as functions (not vars) so a future palette retune flows in
// without a package init dance.
func wordDiffBgRemove() lipgloss.Color { return theme.ColorWordDiffRemoveBg }
func wordDiffBgAdd() lipgloss.Color    { return theme.ColorWordDiffAddBg }
