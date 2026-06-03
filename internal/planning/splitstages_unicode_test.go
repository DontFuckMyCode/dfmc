package planning

import (
	"strings"
	"testing"
)

// TestSplitStagesUnicodeOffset guards against an offset-aliasing bug: splitStages
// found stage-marker byte offsets in strings.ToLower(q) but sliced the ORIGINAL q
// with them. strings.ToLower can change byte length — Turkish 'İ' (U+0130, 2 bytes)
// lowercases to 'i' (1 byte) — so every offset after such a char was shifted,
// cutting segments mid-word. With two 'İ' before a marker the boundary lands on a
// letter (not absorbed by the TrimSpace/Trim cleanup), corrupting the split.
//
// The user writes prompts in Turkish, so 'İ' is a live input. stageMarkerRE is
// already case-insensitive ((?i)), so the fix matches on q directly.
func TestSplitStagesUnicodeOffset(t *testing.T) {
	// "İİ work sonra rest": two İ (4 bytes -> 2 when lowercased) precede the
	// "work" word, so the lower-cased "sonra" offset is 2 bytes early — landing
	// inside "work" and stealing its trailing "k" into the next segment.
	q := "İİ work sonra rest"

	subs := splitStages(q)
	if len(subs) < 2 {
		t.Fatalf("expected at least 2 stage subtasks, got %d: %+v", len(subs), subs)
	}

	// The marker-led segment must begin cleanly at "sonra", not carry a stray
	// "k " stolen from the previous word.
	last := subs[len(subs)-1].Description
	if !strings.HasPrefix(last, "sonra") {
		t.Fatalf("stage segment misaligned by Unicode case-folding: got %q, want it to start with %q", last, "sonra")
	}

	// And the first segment must keep "work" whole.
	first := subs[0].Description
	if strings.Contains(first, "wor") && !strings.Contains(first, "work") {
		t.Fatalf("first segment lost a byte off 'work': %q", first)
	}
}
