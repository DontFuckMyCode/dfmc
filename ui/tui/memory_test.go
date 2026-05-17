package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func newMemoryTestModel() Model {
	return Model{
		tabs:                  []string{"Chat", "Status", "Files", "Patch", "Workflow", "Tools", "Activity", "Memory"},
		activeTab:             7,
		diagnosticPanelsState: newDiagnosticPanelsState(),
	}
}

func sampleMemoryEntries() []types.MemoryEntry {
	return []types.MemoryEntry{
		{ID: "m1", Tier: types.MemoryEpisodic, Category: "qa", Key: "what is dfmc", Value: "code assistant"},
		{ID: "m2", Tier: types.MemorySemantic, Category: "fact", Key: "go.mod", Value: "module path"},
		{ID: "m3", Tier: types.MemoryEpisodic, Category: "qa", Key: "auth flow", Value: "bearer token in header"},
	}
}

// TestMemoryEnterTogglesExpand — Phase H item 3: enter on the
// highlighted row toggles `expandedID`. A second enter on the same row
// collapses it; enter on a different row replaces the expanded ID.
func TestMemoryEnterTogglesExpand(t *testing.T) {
	m := newMemoryTestModel()
	m.memory.entries = sampleMemoryEntries()
	m.memory.scroll = 0

	got, _ := m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyEnter})
	gm := got.(Model)
	if gm.memory.expandedID != "m1" {
		t.Fatalf("first enter should expand m1, got %q", gm.memory.expandedID)
	}

	got, _ = gm.handleMemoryKey(tea.KeyMsg{Type: tea.KeyEnter})
	gm = got.(Model)
	if gm.memory.expandedID != "" {
		t.Fatalf("second enter on same row should collapse, got %q", gm.memory.expandedID)
	}

	gm.memory.scroll = 2
	got, _ = gm.handleMemoryKey(tea.KeyMsg{Type: tea.KeyEnter})
	gm = got.(Model)
	if gm.memory.expandedID != "m3" {
		t.Fatalf("enter on a different row should expand the new id, got %q", gm.memory.expandedID)
	}
}

// TestMemoryRenderShowsExpandedBody — when expandedID matches an
// entry, the panel renders the indented body and a metadata footer
// row underneath the one-line summary.
func TestMemoryRenderShowsExpandedBody(t *testing.T) {
	m := newMemoryTestModel()
	m.memory.entries = []types.MemoryEntry{
		{ID: "long", Tier: types.MemoryEpisodic, Category: "design",
			Key: "auth flow", Value: "first the client sends bearer token\nthen the server verifies it"},
	}
	m.memory.expandedID = "long"
	out := m.renderMemoryView(120)
	if !strings.Contains(out, "first the client sends bearer token") {
		t.Fatalf("expected expanded body line in render, got:\n%s", out)
	}
	if !strings.Contains(out, "category=design") {
		t.Fatalf("expected metadata footer with category, got:\n%s", out)
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
	line := formatMemoryRow(e, 120, false)
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
	line := formatMemoryRow(e, 200, false)
	if strings.Contains(line, "\n") {
		t.Fatalf("embedded newline leaked into row: %q", line)
	}
	if !strings.Contains(line, "line with breaks") {
		t.Fatalf("want collapsed text, got %q", line)
	}
}

func TestNextMemoryTierCycles(t *testing.T) {
	// Phase H item 2: cycle is working → episodic → semantic → all →
	// working. Working is the default landing; the loop ends back at
	// working so users can't get stuck in a tier they didn't intend.
	want := []string{
		string(types.MemoryEpisodic),
		string(types.MemorySemantic),
		memoryTierAll,
		string(types.MemoryWorking),
	}
	got := make([]string, 0, len(want))
	tier := string(types.MemoryWorking)
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
	if !strings.Contains(out, "MEMORY") {
		t.Fatalf("empty view missing header:\n%s", out)
	}
	if !strings.Contains(out, "No memory entries") {
		t.Fatalf("empty view missing empty copy:\n%s", out)
	}
}

func TestRenderMemoryViewNoMatchesDistinguishesFromEmpty(t *testing.T) {
	m := newMemoryTestModel()
	m.memory.entries = sampleMemoryEntries()
	m.memory.query = "doesnotexistxyz"
	out := m.renderMemoryView(100)
	if strings.Contains(out, "No memory entries in this view") {
		t.Fatalf("should not render empty copy when entries exist; got:\n%s", out)
	}
	if !strings.Contains(out, "No matches for") {
		t.Fatalf("want no-matches notice, got:\n%s", out)
	}
	if !strings.Contains(out, "Press c to clear the query") {
		t.Fatalf("want clear-query affordance, got:\n%s", out)
	}
}

func TestRenderMemoryViewWithEntries(t *testing.T) {
	m := newMemoryTestModel()
	m.memory.entries = sampleMemoryEntries()
	out := m.renderMemoryView(100)
	if !strings.Contains(out, "1 / 3 shown") || !strings.Contains(out, "3 loaded") {
		t.Fatalf("footer count wrong:\n%s", out)
	}
	if !strings.Contains(out, "bearer token") {
		t.Fatalf("value row missing:\n%s", out)
	}
}

func TestRenderMemoryViewSizedWindowsAroundSelectedEntry(t *testing.T) {
	m := newMemoryTestModel()
	m.memory.entries = []types.MemoryEntry{}
	for i := 0; i < 20; i++ {
		m.memory.entries = append(m.memory.entries, types.MemoryEntry{
			ID:    string(rune('a' + i)),
			Tier:  types.MemoryEpisodic,
			Key:   "memory row " + string(rune('a'+i)),
			Value: "value",
		})
	}
	m.memory.scroll = 15

	out := stripANSI(m.renderMemoryViewSized(100, 10))
	if !strings.Contains(out, "memory row p") {
		t.Fatalf("selected memory row should stay visible, got:\n%s", out)
	}
	if strings.Contains(out, "memory row a") {
		t.Fatalf("memory view should not render from top after scroll, got:\n%s", out)
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

// TestRemoveMemoryEntryByID — Phase H item 1 helper: optimistic local
// delete preserves order and drops only the matching ID. Pinned so a
// future refactor that turns it into a map walk can't silently break
// the order guarantee the panel relies on.
func TestRemoveMemoryEntryByID(t *testing.T) {
	in := []types.MemoryEntry{
		{ID: "m1", Key: "first"},
		{ID: "m2", Key: "second"},
		{ID: "m3", Key: "third"},
	}
	out := removeMemoryEntryByID(in, "m2")
	if len(out) != 2 || out[0].ID != "m1" || out[1].ID != "m3" {
		t.Fatalf("expected [m1,m3], got %#v", out)
	}
	// Missing ID — return identical content (input is left untouched
	// even though the helper doesn't mutate the input slice in place).
	out = removeMemoryEntryByID(in, "doesnotexist")
	if len(out) != 3 {
		t.Fatalf("missing ID should not drop entries, got %d", len(out))
	}
}

// TestDeleteSelectedMemoryEntryWithoutSelection — guard the empty-list
// path: pressing 'd' on an empty memory panel surfaces a hint instead
// of crashing or silently no-oping. Engine isn't touched, scroll stays
// 0, and the user is told what to do.
func TestDeleteSelectedMemoryEntryWithoutSelection(t *testing.T) {
	m := newMemoryTestModel()
	out, _ := m.deleteSelectedMemoryEntry()
	if !strings.Contains(out.notice, "No memory entry selected") {
		t.Fatalf("expected guidance notice, got %q", out.notice)
	}
}

// TestPromoteSelectedMemoryEntryNoOpsWhenAlreadySemantic — pressing 'p'
// on a semantic entry shows a friendly notice and skips the engine
// call. Prevents the user from getting an opaque "already semantic"
// error from the bbolt walk for a perfectly valid action.
func TestPromoteSelectedMemoryEntryNoOpsWhenAlreadySemantic(t *testing.T) {
	m := newMemoryTestModel()
	m.memory.entries = []types.MemoryEntry{
		{ID: "m1", Tier: types.MemorySemantic, Key: "fact", Value: "value"},
	}
	out, cmd := m.promoteSelectedMemoryEntry()
	if !strings.Contains(out.notice, "already semantic") {
		t.Fatalf("expected already-semantic notice, got %q", out.notice)
	}
	if cmd != nil {
		t.Fatalf("expected no command for already-semantic promote, got %#v", cmd)
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
