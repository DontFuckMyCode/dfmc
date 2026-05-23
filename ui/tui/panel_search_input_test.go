package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderSearchInput_RendersQueryWithCaret(t *testing.T) {
	out := ansi.Strip(renderSearchInput("foo", "placeholder"))
	if !strings.Contains(out, "Search:") {
		t.Errorf("missing Search: label, got %q", out)
	}
	if !strings.Contains(out, "foo") {
		t.Errorf("missing query, got %q", out)
	}
	if !strings.HasSuffix(out, "▏") {
		t.Errorf("expected trailing caret ▏, got %q", out)
	}
	// Placeholder must NOT appear when query is non-empty.
	if strings.Contains(out, "placeholder") {
		t.Errorf("placeholder should not show when query set, got %q", out)
	}
}

func TestRenderSearchInput_ShowsPlaceholderWhenEmpty(t *testing.T) {
	out := ansi.Strip(renderSearchInput("", "type something…"))
	if !strings.Contains(out, "type something…") {
		t.Errorf("missing placeholder, got %q", out)
	}
}

func TestSearchTypingHint_NamesTheKeys(t *testing.T) {
	out := ansi.Strip(searchTypingHint())
	for _, want := range []string{"enter commit", "esc stop", "backspace delete"} {
		if !strings.Contains(out, want) {
			t.Errorf("hint missing %q, got %q", want, out)
		}
	}
}

func TestPanelIdleHint_SubstitutesEnterVerb(t *testing.T) {
	out := ansi.Strip(panelIdleHint("preview"))
	for _, want := range []string{"↑↓ scroll", "enter preview", "/ search"} {
		if !strings.Contains(out, want) {
			t.Errorf("hint missing %q, got %q", want, out)
		}
	}
	// Re-run verb should be substituted verbatim.
	if !strings.Contains(ansi.Strip(panelIdleHint("re-run")), "enter re-run") {
		t.Errorf("re-run verb missing, got %q", ansi.Strip(panelIdleHint("re-run")))
	}
	// `esc back` was removed from the canonical idle hint because esc
	// never leaves a panel — it only cancels search mode in sub-states.
	if strings.Contains(out, "esc back") {
		t.Errorf("idle hint must not claim 'esc back' (esc only cancels search mode), got %q", out)
	}
}

func TestSearchHitsChip_ZeroAndNonZero(t *testing.T) {
	zero := ansi.Strip(searchHitsChip(0))
	if !strings.Contains(zero, "0 hits") {
		t.Errorf("expected '0 hits' label, got %q", zero)
	}
	nonzero := ansi.Strip(searchHitsChip(42))
	if !strings.Contains(nonzero, "42 hits") {
		t.Errorf("expected '42 hits' label, got %q", nonzero)
	}
}

// TestPerPanelChipAliases_DelegateToShared pins the contract that every
// panel's *HitsChip is a thin forwarder over searchHitsChip — if anyone
// re-introduces a divergent per-panel implementation, the cross-panel
// look-and-feel guarantee breaks and this test catches it.
func TestPerPanelChipAliases_DelegateToShared(t *testing.T) {
	want := searchHitsChip(7)
	for _, c := range []struct {
		name string
		got  string
	}{
		{"activityHitsChip", activityHitsChip(7)},
		{"codemapHitsChip", codemapHitsChip(7)},
		{"conversationsHitsChip", conversationsHitsChip(7)},
		{"memoryHitsChip", memoryHitsChip(7)},
		{"promptsHitsChip", promptsHitsChip(7)},
		{"securityHitsChip", securityHitsChip(7)},
	} {
		if c.got != want {
			t.Errorf("%s diverged from searchHitsChip: got %q want %q", c.name, c.got, want)
		}
	}
}
