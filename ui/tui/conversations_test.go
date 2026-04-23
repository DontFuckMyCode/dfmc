package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func newConversationsTestModel() Model {
	return Model{
		tabs:                  []string{"Chat", "Status", "Files", "Patch", "Workflow", "Tools", "Activity", "Memory", "CodeMap", "Conversations"},
		activeTab:             9,
		diagnosticPanelsState: newDiagnosticPanelsState(),
	}
}

func sampleConversationSummaries() []conversation.Summary {
	start := time.Date(2026, 4, 16, 9, 30, 0, 0, time.UTC)
	return []conversation.Summary{
		{ID: "2026-04-16-auth-flow", StartedAt: start, MessageN: 12, Provider: "zai", Model: "glm-5.1"},
		{ID: "2026-04-15-review", StartedAt: start.Add(-24 * time.Hour), MessageN: 6, Provider: "anthropic", Model: "claude-opus-4-7"},
		{ID: "2026-04-14-bugfix", StartedAt: start.Add(-48 * time.Hour), MessageN: 4, Provider: "unknown", Model: "unknown"},
	}
}

func TestFilteredConversationsMatchesIDProviderModel(t *testing.T) {
	entries := sampleConversationSummaries()
	got := filteredConversations(entries, "auth")
	if len(got) != 1 || got[0].ID != "2026-04-16-auth-flow" {
		t.Fatalf("ID match failed, got %#v", got)
	}
	got = filteredConversations(entries, "anthropic")
	if len(got) != 1 || got[0].ID != "2026-04-15-review" {
		t.Fatalf("provider match failed, got %#v", got)
	}
	got = filteredConversations(entries, "glm-5.1")
	if len(got) != 1 || got[0].ID != "2026-04-16-auth-flow" {
		t.Fatalf("model match failed, got %#v", got)
	}
	got = filteredConversations(entries, "")
	if len(got) != 3 {
		t.Fatalf("empty query should return all, got %d", len(got))
	}
}

func TestFormatConversationRowShapesLine(t *testing.T) {
	s := sampleConversationSummaries()[0]
	row := formatConversationRow(s, false, 120)
	for _, want := range []string{"12 msgs", "zai", "glm-5.1", "2026-04-16-auth-flow"} {
		if !strings.Contains(row, want) {
			t.Errorf("row missing %q: %s", want, row)
		}
	}
}

func TestFormatConversationRowHighlightsSelected(t *testing.T) {
	s := sampleConversationSummaries()[0]
	selected := formatConversationRow(s, true, 120)
	unselected := formatConversationRow(s, false, 120)
	if !strings.Contains(selected, "▶") {
		t.Fatalf("selected row should carry arrow marker, got %q", selected)
	}
	if strings.Contains(unselected, "▶") {
		t.Fatalf("unselected row should NOT carry arrow marker, got %q", unselected)
	}
}

func TestFormatConversationRowElidesUnknownProvider(t *testing.T) {
	s := sampleConversationSummaries()[2]
	row := formatConversationRow(s, false, 200)
	if strings.Contains(row, "unknown") {
		t.Fatalf("unknown provider/model should be elided, got %q", row)
	}
}

func TestFormatConversationPreviewCollapsesWhitespace(t *testing.T) {
	msgs := []types.Message{
		{Role: types.RoleUser, Content: "line\n\twith\nbreaks"},
		{Role: types.RoleAssistant, Content: "ok"},
	}
	out := formatConversationPreview(msgs, 200)
	if len(out) != 2 {
		t.Fatalf("want 2 preview rows, got %d", len(out))
	}
	if strings.Contains(out[0], "\n") {
		t.Fatalf("embedded newline leaked: %q", out[0])
	}
	if !strings.Contains(out[0], "line with breaks") {
		t.Fatalf("want collapsed text, got %q", out[0])
	}
}

func TestFormatConversationPreviewEmpty(t *testing.T) {
	out := formatConversationPreview(nil, 80)
	if len(out) != 1 || !strings.Contains(out[0], "empty transcript") {
		t.Fatalf("empty preview should yield placeholder, got %v", out)
	}
}

func TestRenderConversationsViewEmptyState(t *testing.T) {
	m := newConversationsTestModel()
	out := m.renderConversationsView(80)
	if !strings.Contains(out, "Conversations") {
		t.Fatalf("header missing:\n%s", out)
	}
	if !strings.Contains(out, "No conversations persisted yet") {
		t.Fatalf("empty copy missing:\n%s", out)
	}
	if !strings.Contains(out, ".dfmc/conversations/") {
		t.Fatalf("empty state should point at the conversations directory, got:\n%s", out)
	}
}

func TestRenderConversationsViewNoMatchesDistinguishesFromEmpty(t *testing.T) {
	m := newConversationsTestModel()
	m.conversations.entries = sampleConversationSummaries()
	m.conversations.query = "doesnotexistxyz"
	out := m.renderConversationsView(120)
	if strings.Contains(out, "No conversations persisted yet") {
		t.Fatalf("should not render empty copy when entries exist; got:\n%s", out)
	}
	if !strings.Contains(out, "No matches for") {
		t.Fatalf("want no-matches notice, got:\n%s", out)
	}
	if !strings.Contains(out, "Press c to clear the query") {
		t.Fatalf("want clear-query affordance, got:\n%s", out)
	}
}

func TestRenderConversationsViewErrorBanner(t *testing.T) {
	m := newConversationsTestModel()
	m.conversations.err = "store locked"
	out := m.renderConversationsView(80)
	if !strings.Contains(out, "error · store locked") {
		t.Fatalf("error banner missing:\n%s", out)
	}
}

func TestRenderConversationsViewWithEntries(t *testing.T) {
	m := newConversationsTestModel()
	m.conversations.entries = sampleConversationSummaries()
	out := m.renderConversationsView(120)
	if !strings.Contains(out, "3 shown · 3 loaded") {
		t.Fatalf("footer count wrong:\n%s", out)
	}
	if !strings.Contains(out, "2026-04-16-auth-flow") {
		t.Fatalf("row missing:\n%s", out)
	}
}

func TestConversationsScrollBindings(t *testing.T) {
	m := newConversationsTestModel()
	m.conversations.entries = sampleConversationSummaries()

	m2, _ := m.handleConversationsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m2.(Model)
	if m.conversations.scroll != 1 {
		t.Fatalf("j should advance, got %d", m.conversations.scroll)
	}
	m2, _ = m.handleConversationsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = m2.(Model)
	if m.conversations.scroll != len(m.conversations.entries)-1 {
		t.Fatalf("G should jump to last, got %d", m.conversations.scroll)
	}
	m2, _ = m.handleConversationsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = m2.(Model)
	if m.conversations.scroll != 0 {
		t.Fatalf("g should jump to top, got %d", m.conversations.scroll)
	}
}

func TestConversationsSearchInputFlow(t *testing.T) {
	m := newConversationsTestModel()
	m.conversations.entries = sampleConversationSummaries()

	m2, _ := m.handleConversationsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = m2.(Model)
	if !m.conversations.searchActive {
		t.Fatalf("search mode should activate on /")
	}

	for _, r := range "auth" {
		m2, _ = m.handleConversationsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m2.(Model)
	}
	if m.conversations.query != "auth" {
		t.Fatalf("want query=auth, got %q", m.conversations.query)
	}

	m2, _ = m.handleConversationsKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m2.(Model)
	if m.conversations.query != "aut" {
		t.Fatalf("backspace should trim, got %q", m.conversations.query)
	}

	m2, _ = m.handleConversationsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(Model)
	if m.conversations.searchActive {
		t.Fatalf("enter should exit search mode")
	}
	if m.conversations.scroll != 0 {
		t.Fatalf("enter should reset scroll, got %d", m.conversations.scroll)
	}

	m2, _ = m.handleConversationsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = m2.(Model)
	if m.conversations.query != "" {
		t.Fatalf("c should clear query, got %q", m.conversations.query)
	}
}

// TestConversationPreviewMsgSetsLoadedNotice — the preview arrival handler
// must tell the user that Manager.Load has side-effect activated the
// conversation, not stay silent. Users were complaining that the Chat
// history changed out from under them after they 'just previewed'.
// Post-H2 fix: LoadReadOnly is genuinely read-only, so the notice must
// reflect "previewed, NOT loaded" — the previous "Loaded conversation"
// + "switch to Chat to resume" wording lied about what happened.
func TestConversationPreviewMsgSetsLoadedNotice(t *testing.T) {
	m := newConversationsTestModel()
	next, _ := m.Update(conversationPreviewMsg{
		id:   "2026-04-16-auth-flow",
		msgs: []types.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}},
	})
	mm := next.(Model)
	if mm.conversations.previewID != "2026-04-16-auth-flow" {
		t.Fatalf("preview id not stored, got %q", mm.conversations.previewID)
	}
	if !strings.Contains(mm.notice, "Previewed conversation") {
		t.Fatalf("notice should announce a read-only preview, got %q", mm.notice)
	}
	if !strings.Contains(mm.notice, "2026-04-16-auth-flow") {
		t.Fatalf("notice should include the conversation id, got %q", mm.notice)
	}
	if !strings.Contains(mm.notice, "read-only") {
		t.Fatalf("notice must make read-only contract explicit, got %q", mm.notice)
	}
}

// TestRenderConversationsViewAnnouncesReadOnlyPreview — H2 fix: the
// preview header must NOT claim "loaded as active" because LoadReadOnly
// no longer mutates active state. It must surface the read-only contract
// so users don't expect Chat to switch.
func TestRenderConversationsViewAnnouncesReadOnlyPreview(t *testing.T) {
	m := newConversationsTestModel()
	m.conversations.entries = sampleConversationSummaries()
	m.conversations.previewID = "2026-04-16-auth-flow"
	m.conversations.preview = []types.Message{{Role: "user", Content: "hi"}}
	out := m.renderConversationsView(160)
	if !strings.Contains(out, "read-only") {
		t.Fatalf("preview header must mark itself as read-only, got:\n%s", out)
	}
	if strings.Contains(out, "loaded as active") {
		t.Fatalf("preview header must NOT claim 'loaded as active' — that side-effect was removed in H2, got:\n%s", out)
	}
}

func TestConversationsRefreshSetsLoading(t *testing.T) {
	m := newConversationsTestModel()
	m2, _ := m.handleConversationsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = m2.(Model)
	if !m.conversations.loading {
		t.Fatalf("r should set loading=true")
	}
	if m.conversations.err != "" {
		t.Fatalf("r should clear error, got %q", m.conversations.err)
	}
}
