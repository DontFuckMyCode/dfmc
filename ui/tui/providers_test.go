package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func newProvidersTestModel() Model {
	return Model{
		tabs:                  []string{"Chat", "Status", "Files", "Patch", "Setup", "Tools", "Activity", "Memory", "CodeMap", "Conversations", "Prompts", "Security", "Plans", "Context", "Providers"},
		activeTab:             14,
		diagnosticPanelsState: newDiagnosticPanelsState(),
	}
}

func sampleProviderRows() []providerRow {
	return []providerRow{
		{Name: "anthropic", Model: "claude-opus-4", MaxContext: 200000, ToolStyle: "provider-native", SupportsTools: true, BestFor: []string{"reasoning", "long-context"}, IsPrimary: true, Status: "ready"},
		{Name: "deepseek", Model: "deepseek-chat", MaxContext: 128000, ToolStyle: "provider-native", SupportsTools: false, BestFor: []string{"code"}, Status: "no-key"},
		{Name: "offline", Model: "deterministic", MaxContext: 12000, ToolStyle: "none", SupportsTools: false, BestFor: []string{"offline-analysis", "fallback"}, IsOffline: true, Status: "offline"},
	}
}

func TestProviderStatusTagDerivation(t *testing.T) {
	cases := []struct {
		name          string
		supportsTools bool
		wantStatus    string
		wantOffline   bool
	}{
		{"offline", false, "offline", true},
		{"Offline", true, "offline", true}, // name wins over tools flag
		{"anthropic", true, "ready", false},
		{"deepseek", false, "no-key", false},
	}
	for _, c := range cases {
		gotStatus, gotOffline := providerStatusTag(c.name, c.supportsTools)
		if gotStatus != c.wantStatus || gotOffline != c.wantOffline {
			t.Errorf("providerStatusTag(%q, %v) = (%q, %v), want (%q, %v)", c.name, c.supportsTools, gotStatus, gotOffline, c.wantStatus, c.wantOffline)
		}
	}
}

func TestProviderStatusStyleLabels(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"ready", "READY"},
		{"offline", "OFFLINE"},
		{"no-key", "NO-KEY"},
		{"mystery", "MYSTERY"},
	}
	for _, c := range cases {
		got := providerStatusStyle(c.in)
		if !strings.Contains(got, c.want) {
			t.Errorf("providerStatusStyle(%q) missing %q, got %q", c.in, c.want, got)
		}
	}
}

func TestFormatProviderRowContainsSignalFields(t *testing.T) {
	row := sampleProviderRows()[0]
	out := formatProviderRow(row, false, 200)
	for _, want := range []string{"READY", "anthropic", "claude-opus-4", "max=200000", "tools=on", "provider-native"} {
		if !strings.Contains(out, want) {
			t.Errorf("row missing %q: %s", want, out)
		}
	}
}

func TestFormatProviderRowPrimaryStar(t *testing.T) {
	rows := sampleProviderRows()
	primaryOut := formatProviderRow(rows[0], false, 200)
	nonPrimaryOut := formatProviderRow(rows[1], false, 200)
	if !strings.Contains(primaryOut, "*") {
		t.Fatalf("primary row should carry star: %q", primaryOut)
	}
	if strings.Contains(nonPrimaryOut, "*") {
		t.Fatalf("non-primary row should not carry star: %q", nonPrimaryOut)
	}
}

func TestFormatProviderRowHighlightsSelected(t *testing.T) {
	row := sampleProviderRows()[0]
	sel := formatProviderRow(row, true, 200)
	uns := formatProviderRow(row, false, 200)
	if !strings.Contains(sel, "▶") {
		t.Fatalf("selected row missing arrow: %q", sel)
	}
	if strings.Contains(uns, "▶") {
		t.Fatalf("unselected row should not carry arrow: %q", uns)
	}
}

func TestFormatProviderRowToolsOff(t *testing.T) {
	row := sampleProviderRows()[1]
	out := formatProviderRow(row, false, 200)
	if !strings.Contains(out, "tools=off") {
		t.Fatalf("no-key row should show tools=off: %s", out)
	}
	if !strings.Contains(out, "NO-KEY") {
		t.Fatalf("no-key row should show NO-KEY tag: %s", out)
	}
}

func TestFormatProviderDetailNoKeyWarns(t *testing.T) {
	row := sampleProviderRows()[1]
	out := strings.Join(formatProviderDetail(row, 200), "\n")
	for _, want := range []string{"deepseek", "deepseek-chat", "best_for", "code", "missing API key"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail missing %q: %s", want, out)
		}
	}
}

func TestFormatProviderDetailOfflineCopy(t *testing.T) {
	row := sampleProviderRows()[2]
	out := strings.Join(formatProviderDetail(row, 200), "\n")
	if !strings.Contains(out, "offline provider") {
		t.Fatalf("offline detail should explain fallback role: %s", out)
	}
}

func TestRenderProvidersViewEmptyState(t *testing.T) {
	m := newProvidersTestModel()
	out := m.renderProvidersView(100)
	if !strings.Contains(out, "Providers") {
		t.Fatalf("header missing: %s", out)
	}
	if !strings.Contains(out, "degraded startup") {
		t.Fatalf("empty-state copy missing: %s", out)
	}
}

func TestRenderProvidersViewWithRows(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	out := m.renderProvidersView(200)
	for _, want := range []string{"anthropic", "deepseek", "offline", "3 providers", "1 ready", "1 missing keys"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestRenderProvidersViewShowsDetailForSelection(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()
	m.providers.scroll = 1 // deepseek
	out := m.renderProvidersView(200)
	if !strings.Contains(out, "missing API key") {
		t.Fatalf("selecting no-key row should show warn copy: %s", out)
	}
}

func TestRenderProvidersViewErrorBanner(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.err = "router has no providers"
	out := m.renderProvidersView(80)
	if !strings.Contains(out, "error · router has no providers") {
		t.Fatalf("error banner missing: %s", out)
	}
}

func TestRefreshProvidersRowsNilEngineSetsError(t *testing.T) {
	m := newProvidersTestModel()
	m = m.refreshProvidersRows()
	if m.providers.err == "" {
		t.Fatalf("nil engine should set error")
	}
	if m.providers.rows != nil {
		t.Fatalf("nil engine should leave rows nil, got %v", m.providers.rows)
	}
}

func TestProvidersScrollBindings(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.rows = sampleProviderRows()

	m2, _ := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m2.(Model)
	if m.providers.scroll != 1 {
		t.Fatalf("j should advance, got %d", m.providers.scroll)
	}
	m2, _ = m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = m2.(Model)
	if m.providers.scroll != 2 {
		t.Fatalf("G should jump to last, got %d", m.providers.scroll)
	}
	m2, _ = m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = m2.(Model)
	if m.providers.scroll != 0 {
		t.Fatalf("g should jump to top, got %d", m.providers.scroll)
	}
	m2, _ = m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = m2.(Model)
	if m.providers.scroll != 0 {
		t.Fatalf("k at top should clamp to 0, got %d", m.providers.scroll)
	}
}

func TestProvidersRefreshMenuResetsError(t *testing.T) {
	m := newProvidersTestModel()
	m.providers.err = "stale"
	// open menu (enter), navigate to Refresh (index 4 with no rows), select (enter)
	m2, _ := m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = m2.(Model)
	if !m.providers.menuActive {
		t.Fatalf("enter should open menu")
	}
	m.providers.menuIndex = 3 // Refresh is last item when no rows
	m2, _ = m.handleProvidersKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = m2.(Model)
	// nil engine keeps an error, but the specific message should be the
	// "engine not ready" one, not "stale".
	if m.providers.err == "stale" {
		t.Fatalf("refresh should re-derive error, not preserve previous text")
	}
}
