package tokens

import (
	"strings"
	"testing"
)

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
