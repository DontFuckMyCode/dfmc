package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderProgressBar_FillsCorrectFraction(t *testing.T) {
	out := renderProgressBar(3, 10, 10)
	stripped := ansi.Strip(out)
	filled := strings.Count(stripped, "█")
	empty := strings.Count(stripped, "░")
	if filled != 3 {
		t.Errorf("expected 3 filled cells, got %d (%q)", filled, stripped)
	}
	if empty != 7 {
		t.Errorf("expected 7 empty cells, got %d (%q)", empty, stripped)
	}
}

func TestRenderProgressBar_HandlesEdgeCases(t *testing.T) {
	// Zero total: empty string (caller responsible for guarding).
	if out := renderProgressBar(0, 0, 10); out != "" {
		t.Errorf("expected empty bar for total=0, got %q", out)
	}
	// Overflow: counter past total → clamp at filled=cells.
	out := renderProgressBar(20, 10, 10)
	stripped := ansi.Strip(out)
	if strings.Count(stripped, "█") != 10 {
		t.Errorf("overflow should clamp to full bar, got %q", stripped)
	}
}

func TestRenderRunProgressChip_FormatsPercentAndCount(t *testing.T) {
	out := renderRunProgressChip(3, 10)
	stripped := ansi.Strip(out)
	for _, want := range []string{"3/10", "30%"} {
		if !strings.Contains(stripped, want) {
			t.Errorf("expected %q in progress chip, got %q", want, stripped)
		}
	}
}

func TestRenderRunProgressChip_AnnouncesCompletion(t *testing.T) {
	out := renderRunProgressChip(5, 5)
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "100%") {
		t.Errorf("expected 100%% for fully-done run, got %q", stripped)
	}
	if !strings.Contains(stripped, "✓") {
		t.Errorf("expected ✓ marker for completed run, got %q", stripped)
	}
}

func TestRenderRunProgressChip_EmptyRunReportsCleanly(t *testing.T) {
	out := renderRunProgressChip(0, 0)
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "no todos") {
		t.Errorf("expected 'no todos' for total=0, got %q", stripped)
	}
}
