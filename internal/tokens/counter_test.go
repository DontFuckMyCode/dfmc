package tokens

import (
	"strings"
	"testing"
)

func TestHeuristicCountEmpty(t *testing.T) {
	c := NewHeuristic()
	if got := c.Count(""); got != 0 {
		t.Fatalf("empty string: want 0, got %d", got)
	}
	if got := c.Count("   \n\t  "); got != 0 {
		t.Fatalf("whitespace-only: want 0 (no content), got %d", got)
	}
}

// Each sample pairs a representative text with the approximate cl100k/Claude
// token count. Assertion is within a tolerance band since we are explicitly
// heuristic — the goal is "within ±15% of reality" which already crushes the
// old word-count estimator (often off by 2-3x on code).
func TestHeuristicCountWithinTolerance(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		approx  int
		tolPct  float64
		message string
	}{
		{
			name:    "prose_short",
			text:    "The quick brown fox jumps over the lazy dog.",
			approx:  11,
			tolPct:  0.30,
			message: "simple English prose",
		},
		{
			name: "prose_paragraph",
			text: "DFMC is a code intelligence assistant written in Go. " +
				"It combines local code analysis with a provider router " +
				"that can fall back to offline mode when API providers " +
				"are unavailable.",
			approx:  40,
			tolPct:  0.25,
			message: "paragraph prose",
		},
		{
			name:    "go_signature",
			text:    "func (e *Engine) Ask(ctx context.Context, question string) (string, error) {",
			approx:  21,
			tolPct:  0.30,
			message: "Go function signature (code is denser than prose)",
		},
		{
			name: "go_block",
			text: `func (e *Engine) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	if e == nil || e.provider == nil {
		return nil, ErrNotReady
	}
	return e.provider.Stream(ctx, req)
}`,
			approx:  60,
			tolPct:  0.30,
			message: "multi-line Go block",
		},
		{
			name:    "json_payload",
			text:    `{"name":"alice","age":30,"roles":["admin","user"],"active":true}`,
			approx:  25,
			tolPct:  0.35,
			message: "dense JSON",
		},
		{
			name:    "single_word",
			text:    "antidisestablishmentarianism",
			approx:  6,
			tolPct:  0.60,
			message: "single long word (BPE splits it)",
		},
	}

	c := NewHeuristic()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Count(tc.text)
			low := float64(tc.approx) * (1 - tc.tolPct)
			high := float64(tc.approx) * (1 + tc.tolPct)
			if float64(got) < low || float64(got) > high {
				t.Errorf("%s: got %d, want within ±%.0f%% of %d (range %.1f..%.1f)",
					tc.message, got, tc.tolPct*100, tc.approx, low, high)
			}
		})
	}
}

// The old estimator was len(strings.Fields(text)). This test pins the new
// heuristic's advantage on code — word-count is typically 2-3x low on dense
// source code. Regressing below the old estimator would be a real loss.
func TestHeuristicBeatsWordCountOnCode(t *testing.T) {
	code := `func parseLocalToolCall(raw string) (localToolCall, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return localToolCall{}, false
	}
	matches := localToolBlockPattern.FindAllStringSubmatch(trimmed, -1)
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		call, ok := parseLocalToolCallJSON(match[1])
		if ok {
			return call, true
		}
	}
	return localToolCall{}, false
}`
	wordCount := len(strings.Fields(code))
	heuristic := NewHeuristic().Count(code)
	if heuristic <= wordCount {
		t.Fatalf("heuristic (%d) should exceed word count (%d) on dense code", heuristic, wordCount)
	}
	// Real tokenizer (cl100k) produces roughly 120-140 tokens for this block.
	// Word count is ~55. Heuristic should land between — somewhere in 90-160.
	if heuristic < 90 || heuristic > 170 {
		t.Errorf("heuristic estimate %d looks off for a ~130-token code block", heuristic)
	}
}

func TestCountMessagesFramingOverhead(t *testing.T) {
	c := NewHeuristic()

	// Same content packed into one message vs split across four should diverge
	// roughly by 3 * PerMessageOverhead, because framing is paid per message.
	single := c.CountMessages([]Message{
		{Role: "user", Content: "one two three four five six seven eight"},
	})
	split := c.CountMessages([]Message{
		{Role: "user", Content: "one two"},
		{Role: "assistant", Content: "three four"},
		{Role: "user", Content: "five six"},
		{Role: "assistant", Content: "seven eight"},
	})
	if split <= single {
		t.Fatalf("split sequence (%d) should cost more than single (%d) due to framing", split, single)
	}
	delta := split - single
	if delta < 6 || delta > 30 {
		t.Errorf("framing overhead delta %d looks unreasonable", delta)
	}
}

func TestCountMessagesEmpty(t *testing.T) {
	c := NewHeuristic()
	if got := c.CountMessages(nil); got != 0 {
		t.Fatalf("nil messages: want 0, got %d", got)
	}
	if got := c.CountMessages([]Message{}); got != 0 {
		t.Fatalf("empty messages: want 0, got %d", got)
	}
}

func TestDefaultSwappable(t *testing.T) {
	orig := Default()
	t.Cleanup(func() { SetDefault(orig) })

	stub := &stubCounter{fixed: 42}
	SetDefault(stub)

	if got := Estimate("anything"); got != 42 {
		t.Fatalf("SetDefault not wired: got %d want 42", got)
	}
	if got := EstimateMessages([]Message{{Role: "user", Content: "hi"}}); got != 42 {
		t.Fatalf("EstimateMessages not routed through default: got %d", got)
	}
}

type stubCounter struct{ fixed int }

func (s *stubCounter) Count(_ string) int            { return s.fixed }
func (s *stubCounter) CountMessages(_ []Message) int { return s.fixed }
