// Pin REPORT.md C1: trimToolPayload must not split multi-byte UTF-8
// sequences mid-rune. The pre-fix code byte-sliced based on a "max
// chars" parameter, so any tool result containing CJK / emoji /
// accented Latin would surface as broken UTF-8 in the conversation
// JSONL and downstream JSON serializers.

package engine

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTrimToolPayload_PreservesUTF8WhenTruncating(t *testing.T) {
	// Each emoji is 4 bytes — a byte-based truncation at any odd char
	// boundary would split the surrogate pair and leave invalid UTF-8.
	body := strings.Repeat("🦀🐹🦔", 200) // 600 emoji → 2400 bytes
	out := trimToolPayload(body, 50)

	if !utf8.ValidString(out) {
		t.Fatalf("trimToolPayload produced invalid UTF-8 (byte slicing through a multi-byte rune):\n%q", out)
	}
	if got := len([]rune(out)); got > 50 {
		t.Fatalf("trimmed output exceeded the rune budget: got %d runes (cap 50)", got)
	}
	if !strings.HasSuffix(out, "[truncated]") {
		t.Fatalf("expected truncation marker; got %q", out)
	}
}

// CJK + Latin mix — the marker reservation logic must still leave
// room for the suffix without overflowing the cap.
func TestTrimToolPayload_CJKMixWithMarker(t *testing.T) {
	body := strings.Repeat("こんにちは hello ", 100)
	out := trimToolPayload(body, 30)
	if !utf8.ValidString(out) {
		t.Fatalf("invalid UTF-8 after trim: %q", out)
	}
	if got := len([]rune(out)); got > 30 {
		t.Fatalf("rune count %d exceeds cap 30: %q", got, out)
	}
}

// Tiny budget: not enough room for the marker. Must not panic, must
// not overflow, must return valid UTF-8.
func TestTrimToolPayload_TinyBudget(t *testing.T) {
	out := trimToolPayload("こんにちは", 3)
	if !utf8.ValidString(out) {
		t.Fatalf("invalid UTF-8 at tiny budget: %q", out)
	}
	if got := len([]rune(out)); got > 3 {
		t.Fatalf("tiny budget overflowed: got %d runes, cap 3", got)
	}
}

// Happy path — under-budget input passes through untouched.
func TestTrimToolPayload_NoTruncationUnderBudget(t *testing.T) {
	in := "  hello world  "
	out := trimToolPayload(in, 100)
	if out != "hello world" {
		t.Fatalf("under-budget path should return TrimSpace(in); got %q", out)
	}
}

// strconvQuote (M1) must escape control characters. The previous
// hand-rolled version only handled \\ and " — newlines, tabs, and
// other low-byte control chars passed through and broke single-line
// previews and downstream JSON.
func TestStrconvQuote_EscapesControlChars(t *testing.T) {
	in := "line one\nline two\twith tab\rcr"
	out := strconvQuote(in)
	for _, raw := range []string{"\n", "\t", "\r"} {
		if strings.Contains(out, raw) {
			t.Fatalf("strconvQuote leaked raw control char %q in output: %q", raw, out)
		}
	}
	for _, esc := range []string{`\n`, `\t`, `\r`} {
		if !strings.Contains(out, esc) {
			t.Fatalf("strconvQuote missing escape sequence %s in output: %q", esc, out)
		}
	}
	if !strings.HasPrefix(out, `"`) || !strings.HasSuffix(out, `"`) {
		t.Fatalf("strconvQuote should produce a quoted Go string; got %q", out)
	}
}

// L2 (REPORT.md): when memory load fails at Init, the system prompt
// must explicitly tell the model recall is offline so it doesn't
// silently treat "no recall results" as "this project has no memory
// yet" and start writing notes that won't survive the next session.
func TestMemoryDegradedSystemNotice_NamesTheReason(t *testing.T) {
	out := memoryDegradedSystemNotice("bolt: database is locked by another process")
	if !strings.Contains(out, "Memory store is offline") {
		t.Fatalf("notice should announce the gate: %q", out)
	}
	if !strings.Contains(out, "do not rely on historical recall") {
		t.Fatalf("notice should warn against relying on recall: %q", out)
	}
	if !strings.Contains(out, "bolt: database is locked") {
		t.Fatalf("notice should carry the reason verbatim so the model can decide if recoverable: %q", out)
	}
}

func TestMemoryDegradedSystemNotice_FallbackReason(t *testing.T) {
	out := memoryDegradedSystemNotice("")
	if !strings.Contains(out, "store unavailable") {
		t.Fatalf("empty reason should fall back to 'store unavailable': %q", out)
	}
}

// M2 (REPORT.md): the budget-exhausted notice was inlined at 4 sites
// with subtly different field orderings. The helper must emit one
// canonical shape per phase so wording fixes don't drift across copies.
func TestFormatBudgetExhaustedNotice_PhaseShapes(t *testing.T) {
	before := formatBudgetExhaustedNotice(parkPhaseBefore, 7, 1500, 2000, 200, 4)
	if !strings.Contains(before, "before step 7") {
		t.Fatalf("before-phase notice should name the upcoming step: %q", before)
	}
	if !strings.Contains(before, "need ~200 headroom") {
		t.Fatalf("before-phase notice should report the missing headroom: %q", before)
	}
	if !strings.Contains(before, "/continue") {
		t.Fatalf("notice must include the recovery hint: %q", before)
	}

	after := formatBudgetExhaustedNotice(parkPhaseAfter, 12, 1900, 2000, 0, 6)
	if !strings.Contains(after, "after step 12") {
		t.Fatalf("after-phase notice should name the just-completed step: %q", after)
	}
	// The shared recovery suffix uses the word "headroom"; what
	// distinguishes the after-phase shape is the absence of a
	// "need ~X headroom" diagnostic in the leading sentence.
	if strings.Contains(after, "need ~") {
		t.Fatalf("after-phase notice must not report needed headroom (no longer meaningful post-overflow): %q", after)
	}
	if !strings.Contains(after, "/continue") {
		t.Fatalf("after-phase notice must keep the recovery hint: %q", after)
	}
}
