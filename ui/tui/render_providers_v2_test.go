package tui

import (
	"strings"
	"testing"
)

// TestProvidersListV2_RendersAllThreePanesOnWideTerminal — wide
// terminal must show the cleaned-up list rows AND the DETAIL card
// AND the ROUTING/ACTIONS cards. Each fact lives in exactly one
// place — list rows are now scannable, not crammed.
func TestProvidersListV2_RendersAllThreePanesOnWideTerminal(t *testing.T) {
	m := newCoverageModel(t)
	m.providers.rows = []providerRow{
		{
			Name: "anthropic", Model: "claude-sonnet-4-6", Protocol: "anthropic",
			MaxContext: 200000, ToolStyle: "anthropic", SupportsTools: true,
			BestFor: []string{"code", "reasoning"},
			IsPrimary: true, Status: "ready",
			Models: []string{"claude-sonnet-4-6", "claude-opus-4-7"},
		},
		{
			Name: "openai", Model: "gpt-4o", Protocol: "openai",
			MaxContext: 128000, ToolStyle: "openai", SupportsTools: true,
			Status: "no-key", Models: []string{"gpt-4o", "gpt-4o-mini"},
		},
	}
	view := stripANSI(m.renderProviderListViewV2(140))
	for _, want := range []string{
		"PROVIDERS",
		"anthropic", "openai",
		"PRIMARY",
		"READY", "NO KEY",
		"DETAIL",
		"Status:", "Model:", "Protocol:", "Max context:", "Tool support:",
		"ROUTING",
		"ACTIONS",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("wide V2 view missing %q. Got:\n%s", want, view)
		}
	}
}

// TestProvidersListV2_NoKeyHintExplainsRecovery — when the highlighted
// provider has no API key, the detail card should spell out exactly
// what env var to set or yaml key to add.
func TestProvidersListV2_NoKeyHintExplainsRecovery(t *testing.T) {
	m := newCoverageModel(t)
	m.providers.rows = []providerRow{
		{Name: "anthropic", Status: "no-key"},
	}
	view := stripANSI(m.renderProviderListViewV2(140))
	if !strings.Contains(view, "Missing API key") {
		t.Errorf("expected missing-key warning. Got:\n%s", view)
	}
	// Either env var hint or yaml hint must appear.
	if !strings.Contains(view, "ANTHROPIC_API_KEY") && !strings.Contains(view, "providers.profiles.anthropic") {
		t.Errorf("expected recovery hint (env var or yaml). Got:\n%s", view)
	}
}

// TestProvidersListV2_EmptyStatePointsAtNewProvider — when no
// providers are registered the empty state should mention the action
// menu's "Add new provider" entry rather than leaving the user stuck.
func TestProvidersListV2_EmptyStatePointsAtNewProvider(t *testing.T) {
	m := newCoverageModel(t)
	view := stripANSI(m.renderProviderListViewV2(140))
	for _, want := range []string{
		"PROVIDERS",
		"No providers registered",
		"new provider",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("empty V2 view missing %q. Got:\n%s", want, view)
		}
	}
}

// TestProvidersPanelWidths_BreakpointsBehave pins the 3/2/1-pane math.
func TestProvidersPanelWidths_BreakpointsBehave(t *testing.T) {
	t.Run("three-pane", func(t *testing.T) {
		l, d, m := providersPanelWidths(140, true, false)
		if l < 26 || d < 32 || m < 28 {
			t.Errorf("three-pane below floor: l=%d d=%d m=%d", l, d, m)
		}
		if l+d+m+4 > 140 {
			t.Errorf("three-pane overflow: l=%d d=%d m=%d", l, d, m)
		}
	})
	t.Run("two-pane", func(t *testing.T) {
		l, d, mw := providersPanelWidths(100, false, true)
		if mw != 0 {
			t.Errorf("two-pane should have meta=0, got %d", mw)
		}
		if l < 26 || d < 28 {
			t.Errorf("two-pane below floor: l=%d d=%d", l, d)
		}
	})
	t.Run("one-pane", func(t *testing.T) {
		l, d, mw := providersPanelWidths(60, false, false)
		if l != 60 || d != 60 || mw != 0 {
			t.Errorf("one-pane should give full width: l=%d d=%d m=%d", l, d, mw)
		}
	})
}

// TestProvidersListV2_RowsShowOnlyNameAndStatus — load-bearing
// readability promise: each row must NOT cram model/max/tools/etc.
// onto the same line. Those facts move to the DETAIL card.
func TestProvidersListV2_RowsShowOnlyNameAndStatus(t *testing.T) {
	m := newCoverageModel(t)
	m.providers.rows = []providerRow{
		{Name: "anthropic", Model: "claude-sonnet-4-6", MaxContext: 200000, SupportsTools: true, Status: "ready"},
	}
	pal := paletteForTab("Providers", false)
	row := stripANSI(m.renderProviderListRowV2(m.providers.rows[0], 0, 0, 60, pal))

	// Name + status MUST be in the row.
	if !strings.Contains(row, "anthropic") {
		t.Errorf("row missing name. Got %q", row)
	}
	if !strings.Contains(row, "READY") {
		t.Errorf("row missing status chip. Got %q", row)
	}
	// Per-row noise from the legacy renderer must be GONE — those
	// facts only belong in the detail card now.
	for _, gone := range []string{"max=200000", "tools=true", "models=", "key:ok"} {
		if strings.Contains(row, gone) {
			t.Errorf("row should not show %q anymore (it belongs in DETAIL). Got %q",
				gone, row)
		}
	}
}
