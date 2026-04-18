package engine

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/provider"
)

// trace builds a minimal nativeToolTrace for these tests. Most fields
// are zero — the parked notice helpers only read .Call.Name.
func trace(name string) nativeToolTrace {
	return nativeToolTrace{Call: provider.ToolCall{Name: name}}
}

func TestSummarizeTraces_EmptyReturnsEmpty(t *testing.T) {
	if got := summarizeTraces(nil); got != "" {
		t.Fatalf("nil traces should produce empty string, got %q", got)
	}
	if got := summarizeTraces([]nativeToolTrace{}); got != "" {
		t.Fatalf("empty traces should produce empty string, got %q", got)
	}
}

func TestSummarizeTraces_OrdersByCountThenName(t *testing.T) {
	traces := []nativeToolTrace{
		trace("read_file"), trace("read_file"), trace("read_file"), trace("read_file"),
		trace("edit_file"), trace("edit_file"),
		trace("run_command"),
	}
	got := summarizeTraces(traces)
	for _, want := range []string{"read_file×4", "edit_file×2", "run_command", "Did:", "Open: paused after run_command"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q: %s", want, got)
		}
	}
	// Highest-count tool must appear before lower-count tools.
	if i, j := strings.Index(got, "read_file×4"), strings.Index(got, "edit_file×2"); i < 0 || j < 0 || i > j {
		t.Errorf("read_file×4 should precede edit_file×2: %s", got)
	}
}

func TestSummarizeTraces_TruncatesLongList(t *testing.T) {
	traces := []nativeToolTrace{
		trace("a"), trace("b"), trace("c"), trace("d"), trace("e"), trace("f"),
	}
	got := summarizeTraces(traces)
	if !strings.Contains(got, "+2 more") {
		t.Errorf("6 distinct tools should collapse trailing into +2 more, got: %s", got)
	}
}

func TestSummarizeTraces_DroppedNameFallsBackToUnknown(t *testing.T) {
	got := summarizeTraces([]nativeToolTrace{trace("")})
	if !strings.Contains(got, "unknown") {
		t.Errorf("blank tool name should render as unknown, got: %s", got)
	}
}

func TestComposeParkedNotice_WeavesAllParts(t *testing.T) {
	out := composeParkedNotice(
		"Parked at step 60 — hit ceiling.",
		[]nativeToolTrace{trace("read_file"), trace("edit_file")},
		"Type devam to continue.",
	)
	for _, want := range []string{
		"Parked at step 60",
		"Did: edit_file, read_file", // alphabetical tiebreak when counts equal
		"Type devam to continue.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("composed notice missing %q:\n%s", want, out)
		}
	}
	// All three parts should be on separate lines so the TUI can render
	// them as distinct visual blocks.
	if got := strings.Count(out, "\n"); got < 2 {
		t.Errorf("expected ≥2 newlines between sections, got %d:\n%s", got, out)
	}
}

func TestComposeParkedNotice_SkipsEmptyTraceSummary(t *testing.T) {
	out := composeParkedNotice("Parked.", nil, "Resume hint.")
	if strings.Contains(out, "Did:") {
		t.Errorf("empty traces should produce no Did: line, got: %s", out)
	}
	if !strings.Contains(out, "Resume hint.") {
		t.Errorf("resume hint must still appear: %s", out)
	}
}

func TestFormatBudgetExhaustedNotice_BeforeAndAfter(t *testing.T) {
	before := formatBudgetExhaustedNotice(parkPhaseBefore, 7, 12000, 15000, 1000, 5)
	for _, want := range []string{"before step 7", "12000/15000", "need ~1000", "5 rounds", "devam"} {
		if !strings.Contains(before, want) {
			t.Errorf("before-notice missing %q: %s", want, before)
		}
	}
	after := formatBudgetExhaustedNotice(parkPhaseAfter, 8, 16000, 15000, 0, 6)
	for _, want := range []string{"after step 8", "16000/15000", "6 rounds"} {
		if !strings.Contains(after, want) {
			t.Errorf("after-notice missing %q: %s", want, after)
		}
	}
	if strings.Contains(after, "headroom") {
		t.Errorf("after-notice should not mention headroom (it's moot post-overshoot): %s", after)
	}
}
