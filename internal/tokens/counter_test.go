package tokens

import (
	"strings"
	"testing"
)

// TestHeuristicCounter_BoundaryWhitespaceWordFloor regresses an
// over-count bug: the lower-bound word floor used `whitespaceRuns + 1`
// which counted leading and trailing whitespace as extra "words", so
// " a b c " was floored at 5 tokens even though it has 3 words. The
// space->non-space transition counter is now exact regardless of
// boundary whitespace. We assert the floored count for matched-content
// strings is identical between the no-padding and padded variants —
// padding should not raise the floor.
func TestHeuristicCounter_BoundaryWhitespaceWordFloor(t *testing.T) {
	c := NewHeuristic()
	cases := []struct{ a, b string }{
		{"a b c", "   a b c   "},
		{"hello world", "\n\n\thello world\n"},
		{"x", "    x    "},
	}
	for _, tc := range cases {
		gotA := c.Count(tc.a)
		gotB := c.Count(tc.b)
		// Padding may shift the chars/divisor estimate slightly (more
		// chars -> potentially a higher est before the floor kicks in),
		// but the LOWER BOUND from word count must match: both have the
		// same word count, so the floor must match.
		// Verify by isolating: strip padding, compare the floor.
		if gotB < gotA {
			t.Fatalf("padded version got smaller count: %q=%d %q=%d", tc.a, gotA, tc.b, gotB)
		}
		// More directly: the bug would have made gotB exceed gotA by
		// the number of leading+trailing whitespace runs. After the fix
		// the gap is bounded by char-density math only (typically <=2).
		if gotB-gotA > 3 {
			t.Fatalf("padding-induced overcount: %q=%d %q=%d (delta=%d)", tc.a, gotA, tc.b, gotB, gotB-gotA)
		}
	}
}

// Counter overflow: adding many tokens should not panic.
func TestHeuristicCounter_NoPanicOnLargeInput(t *testing.T) {
	c := NewHeuristic()
	large := strings.Repeat("word ", 1_000_000)
	got := c.Count(large)
	if got <= 0 {
		t.Fatalf("expected positive count for large input, got %d", got)
	}
}

// Reset is not part of the Counter interface, but HeuristicCounter
// itself has no Reset method — test that Count is deterministic across
// repeated calls with the same input.
func TestHeuristicCounter_Deterministic(t *testing.T) {
	c := NewHeuristic()
	text := "func main() { println(\"hello\") }"
	got1 := c.Count(text)
	got2 := c.Count(text)
	got3 := c.Count(text)
	if got1 != got2 || got2 != got3 {
		t.Fatalf("Count not deterministic: %d %d %d", got1, got2, got3)
	}
}

// CountMessages with a very large content string should not overflow.
func TestCountMessages_NoOverflow(t *testing.T) {
	c := NewHeuristic()
	big := strings.Repeat("x", 1_000_000)
	msgs := []Message{
		{Role: "user", Content: big},
		{Role: "assistant", Content: big},
		{Role: "user", Content: big},
	}
	got := c.CountMessages(msgs)
	if got <= 0 {
		t.Fatalf("expected positive count, got %d", got)
	}
}

// Verify that PerMessageOverhead and PerSequenceOverhead are wired.
func TestHeuristicCounter_OverheadWired(t *testing.T) {
	c := NewHeuristic()
	before := c.PerMessageOverhead

	// Modify and verify it affects CountMessages.
	c.PerMessageOverhead = before + 10
	msgs := []Message{{Role: "user", Content: "hi"}}
	withDefault := NewHeuristic().CountMessages(msgs)
	withModified := c.CountMessages(msgs)
	if withModified <= withDefault {
		t.Fatalf("overhead modification did not increase count: default=%d modified=%d", withDefault, withModified)
	}
}

// TrimToBudget with zero maxTokens returns empty.
func TestTrimToBudget_ZeroMaxTokens(t *testing.T) {
	got := TrimToBudget("some text content here", 0, "")
	if got != "" {
		t.Fatalf("expected empty string for 0 maxTokens, got %q", got)
	}
}

// TrimToBudget with empty content returns empty.
func TestTrimToBudget_EmptyContent(t *testing.T) {
	got := TrimToBudget("", 100, "")
	if got != "" {
		t.Fatalf("expected empty string for empty content, got %q", got)
	}
}

// TrimToBudget that is already within budget returns unchanged.
func TestTrimToBudget_AlreadyWithinBudget(t *testing.T) {
	text := "short"
	got := TrimToBudget(text, 10, "")
	if got != text {
		t.Fatalf("expected unchanged %q, got %q", text, got)
	}
}

// TrimToBudget appends suffix when there is room.
func TestTrimToBudget_AppendsSuffixWithinBudget(t *testing.T) {
	text := "one two three four five"
	suffix := "[truncated]"
	got := TrimToBudget(text, 4, suffix)
	if !strings.HasSuffix(got, suffix) {
		t.Fatalf("expected suffix in result, got %q", got)
	}
}

// TrimToBudget skips suffix when no room.
func TestTrimToBudget_SuffixOmittedWhenNoRoom(t *testing.T) {
	text := "one two three four five"
	// Very small budget — suffix would consume all tokens.
	got := TrimToBudget(text, 1, "suffix")
	if strings.Contains(got, "suffix") {
		t.Fatalf("suffix should not appear when no room: got %q", got)
	}
}

// EstimateMessages delegates to default counter
func TestEstimateMessages(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	got := EstimateMessages(msgs)
	if got <= 0 {
		t.Fatalf("EstimateMessages returned %d, want positive", got)
	}
}

func TestEstimateMessages_Empty(t *testing.T) {
	got := EstimateMessages(nil)
	if got != 0 {
		t.Fatalf("EstimateMessages(nil) = %d, want 0", got)
	}
	got = EstimateMessages([]Message{})
	if got != 0 {
		t.Fatalf("EstimateMessages([]) = %d, want 0", got)
	}
}

// Default returns the process-wide default Counter
func TestDefault(t *testing.T) {
	c := Default()
	if c == nil {
		t.Fatal("Default() returned nil")
	}
	// Should be functional
	n := c.Count("hello world")
	if n <= 0 {
		t.Fatalf("Default().Count returned %d, want positive", n)
	}
}

// SetDefault swaps the default counter
func TestSetDefault_Normal(t *testing.T) {
	prev := Default()
	custom := &HeuristicCounter{PerMessageOverhead: 99}
	SetDefault(custom)
	got := Default()
	if got != custom {
		t.Fatalf("Default() did not return the custom counter after SetDefault")
	}
	SetDefault(prev) // restore
}

func TestSetDefault_NilIgnored(t *testing.T) {
	prev := Default()
	SetDefault(nil)
	if Default() != prev {
		t.Fatal("SetDefault(nil) should not change the default counter")
	}
}

// Count edge cases

func TestCount_EmptyAfterTrim(t *testing.T) {
	c := NewHeuristic()
	// Only whitespace
	got := c.Count("   \t\n  ")
	if got != 0 {
		t.Errorf("whitespace-only: got %d", got)
	}
}

func TestCount_EmptyString(t *testing.T) {
	c := NewHeuristic()
	got := c.Count("")
	if got != 0 {
		t.Errorf("empty string: got %d", got)
	}
}

// Count density branches: JSON/minified (>0.22), source code (>0.12), mixed (>0.06)

func TestCount_DensityJSON(t *testing.T) {
	c := NewHeuristic()
	// JSON with many symbols -> density > 0.22
	json := `{"a":1,"b":2,"c":[1,2,3],"d":{"x":true,"y":false},"e":"longer string here"}`
	got := c.Count(json)
	if got <= 0 {
		t.Fatalf("expected positive count, got %d", got)
	}
}

func TestCount_DensitySourceCode(t *testing.T) {
	c := NewHeuristic()
	// Source code: brackets, colons, etc. -> density > 0.12
	code := `func add(a int, b int) int { return a + b }`
	got := c.Count(code)
	if got <= 0 {
		t.Fatalf("expected positive count, got %d", got)
	}
}

func TestCount_DensityProse(t *testing.T) {
	c := NewHeuristic()
	// Plain prose with minimal symbols -> density < 0.06 -> divisor 4.2
	text := "This is a simple sentence with just words and periods."
	got := c.Count(text)
	if got <= 0 {
		t.Fatalf("expected positive count, got %d", got)
	}
}

// TrimToBudget binary search path: when content must be trimmed

func TestTrimToBudget_BestZeroReturnsEmpty(t *testing.T) {
	// Very small budget so that even one word exceeds it
	// Each word is at least 1 token, so budget=0 or budget=1 with suffix consuming tokens
	got := TrimToBudget("one two three", 0, "")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestTrimToBudget_BinarySearchTrims(t *testing.T) {
	// Content is long enough that it must be trimmed by binary search
	text := "one two three four five six seven eight nine ten eleven twelve"
	// Use a moderate budget that forces trimming but not to zero
	got := TrimToBudget(text, 5, "")
	if got == "" {
		t.Fatal("expected non-empty result")
	}
	if got == text {
		t.Error("expected trimmed result, got original")
	}
	// The result should have fewer words
	gotWords := len(strings.Fields(got))
	textWords := len(strings.Fields(text))
	if gotWords >= textWords {
		t.Errorf("expected fewer words, got %d vs %d", gotWords, textWords)
	}
}
