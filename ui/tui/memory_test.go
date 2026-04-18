package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func newMemoryTestModel() Model {
	return Model{
		tabs:      []string{"Chat", "Status", "Files", "Patch", "Setup", "Tools", "Activity", "Memory"},
		activeTab: 7,
		memory:    memoryPanelState{tier: memoryTierAll},
	}
}

func sampleMemoryEntries() []types.MemoryEntry {
	return []types.MemoryEntry{
		{ID: "m1", Tier: types.MemoryEpisodic, Category: "qa", Key: "what is dfmc", Value: "code assistant"},
		{ID: "m2", Tier: types.MemorySemantic, Category: "fact", Key: "go.mod", Value: "module path"},
		{ID: "m3", Tier: types.MemoryEpisodic, Category: "qa", Key: "auth flow", Value: "bearer token in header"},
	}
}

func TestFilteredMemoryEntriesMatchesCategoryKeyValue(t *testing.T) {
	entries := sampleMemoryEntries()
	got := filteredMemoryEntries(entries, "auth")
	if len(got) != 1 || got[0].ID != "m3" {
		t.Fatalf("value match failed, got %#v", got)
	}
	got = filteredMemoryEntries(entries, "fact")
	if len(got) != 1 || got[0].ID != "m2" {
		t.Fatalf("category match failed, got %#v", got)
	}
	got = filteredMemoryEntries(entries, "")
	if len(got) != 3 {
		t.Fatalf("empty query should return all, got %d", len(got))
	}
}

func TestFormatMemoryRowShapesLineWithTier(t *testing.T) {
	e := types.MemoryEntry{Tier: types.MemoryEpisodic, Category: "qa", Key: "x", Value: "y"}
	line := formatMemoryRow(e, 120)
	if !strings.Contains(line, "[EPISODIC]") {
		t.Fatalf("expected tier label, got %q", line)
	}
	if !strings.Contains(line, "qa") || !strings.Contains(line, "y") {
		t.Fatalf("expected category + value, got %q", line)
	}
}

func TestFormatMemoryRowCollapsesWhitespace(t *testing.T) {
	e := types.MemoryEntry{
		Tier:  types.MemorySemantic,
		Key:   "multi",
		Value: "line\n\twith\nbreaks",
	}
	line := formatMemoryRow(e, 200)
	if strings.Contains(line, "\n") {
		t.Fatalf("embedded newline leaked into row: %q", line)
	}
	if !strings.Contains(line, "line with breaks") {
		t.Fatalf("want collapsed text, got %q", line)
	}
}

func TestNextMemoryTierCycles(t *testing.T) {
	want := []string{
		string(types.MemoryEpisodic),
		string(types.MemorySemantic),
		memoryTierAll,
		string(types.MemoryEpisodic),
	}
	got := make([]string, 0, len(want))
	tier := memoryTierAll
	for i := 0; i < len(want); i++ {
		tier = nextMemoryTier(tier)
		got = append(got, tier)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d: want %q got %q", i, want[i], got[i])
		}
	}
}

func TestMemoryScrollBindings(t *testing.T) {
	m := newMemoryTestModel()
	m.memory.entries = sampleMemoryEntries()

	m2, _ := m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m2.(Model)
	if m.memory.scroll != 1 {
		t.Fatalf("j should advance scroll, got %d", m.memory.scroll)
	}
	m2, _ = m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = m2.(Model)
	if m.memory.scroll != len(m.memory.entries)-1 {
		t.Fatalf("G should jump to last, got %d", m.memory.scroll)
	}
	m2, _ = m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = m2.(Model)
	if m.memory.scroll != 0 {
		t.Fatalf("g should jump to top, got %d", m.memory.scroll)
	}
}

func TestMemorySearchInputFlow(t *testing.T) {
	m := newMemoryTestModel()
	m.memory.entries = sampleMemoryEntries()

	// `/` enters search mode.
	m2, _ := m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = m2.(Model)
	if !m.memory.searchActive {
		t.Fatalf("search mode should activate on /")
	}

	// Type "auth" rune by rune.
	for _, r := range "auth" {
		m2, _ = m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m2.(Model)
	}
	if m.memory.query != "auth" {
		t.Fatalf("want query=auth, got %q", m.memory.query)
	}

	// Backspace trims one rune.
	m2, _ = m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m2.(Model)
	if m.memory.query != "aut" {
		t.Fatalf("backspace should trim, got %q", m.memory.query)
	}

	// Enter commits; search mode exits.
	m2, _ = m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(Model)
	if m.memory.searchActive {
		t.Fatalf("enter should exit search mode")
	}
	if m.memory.scroll != 0 {
		t.Fatalf("enter should reset scroll, got %d", m.memory.scroll)
	}

	// `c` clears the committed query.
	m2, _ = m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = m2.(Model)
	if m.memory.query != "" {
		t.Fatalf("c should clear query, got %q", m.memory.query)
	}
}

func TestRenderMemoryViewEmptyState(t *testing.T) {
	m := newMemoryTestModel()
	out := m.renderMemoryView(80)
	if !strings.Contains(out, "Memory") {
		t.Fatalf("empty view missing header:\n%s", out)
	}
	if !strings.Contains(out, "No memory entries") {
		t.Fatalf("empty view missing empty copy:\n%s", out)
	}
}

func TestRenderMemoryViewWithEntries(t *testing.T) {
	m := newMemoryTestModel()
	m.memory.entries = sampleMemoryEntries()
	out := m.renderMemoryView(100)
	if !strings.Contains(out, "3 shown · 3 loaded") {
		t.Fatalf("footer count wrong:\n%s", out)
	}
	if !strings.Contains(out, "bearer token") {
		t.Fatalf("value row missing:\n%s", out)
	}
}

func TestRenderMemoryViewErrorBanner(t *testing.T) {
	m := newMemoryTestModel()
	m.memory.err = "db locked"
	out := m.renderMemoryView(80)
	if !strings.Contains(out, "error · db locked") {
		t.Fatalf("error banner missing:\n%s", out)
	}
}

func TestMemoryTierToggleRequestsReload(t *testing.T) {
	m := newMemoryTestModel()
	m2, cmd := m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = m2.(Model)
	if m.memory.tier != string(types.MemoryEpisodic) {
		t.Fatalf("first toggle should land on episodic, got %q", m.memory.tier)
	}
	if !m.memory.loading {
		t.Fatalf("toggle should request reload (set loading)")
	}
	// cmd is nil when eng is nil — we're asserting the intent, not the
	// engine call, because Model.eng isn't populated in these tests.
	_ = cmd
}
