package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

func newPromptsTestModel() Model {
	return Model{
		tabs:      []string{"Chat", "Status", "Files", "Patch", "Setup", "Tools", "Activity", "Memory", "CodeMap", "Conversations", "Prompts"},
		activeTab: 10,
	}
}

func samplePromptTemplates() []promptlib.Template {
	return []promptlib.Template{
		{
			ID: "review.base", Type: "review", Task: "review",
			Role: "senior", Language: "go", Profile: "",
			Compose: "replace", Priority: 10,
			Description: "Base review prompt",
			Body:        "You are a senior Go reviewer.\nCall out bugs first.",
		},
		{
			ID: "explain.base", Type: "explain", Task: "explain",
			Role: "teacher", Language: "", Profile: "tight",
			Compose: "append", Priority: 5,
			Description: "Teaches code bits",
			Body:        "Explain step by step.",
		},
		{
			ID: "context.header", Type: "context",
			Priority: 0, Description: "", Body: "",
		},
	}
}

func TestFilteredPromptsMatchesMetadata(t *testing.T) {
	all := samplePromptTemplates()
	got := filteredPrompts(all, "review")
	if len(got) != 1 || got[0].ID != "review.base" {
		t.Fatalf("type/task match failed, got %#v", got)
	}
	got = filteredPrompts(all, "teacher")
	if len(got) != 1 || got[0].ID != "explain.base" {
		t.Fatalf("role match failed, got %#v", got)
	}
	got = filteredPrompts(all, "go")
	if len(got) != 1 || got[0].Language != "go" {
		t.Fatalf("language match failed, got %#v", got)
	}
	got = filteredPrompts(all, "")
	if len(got) != 3 {
		t.Fatalf("empty query should pass through, got %d", len(got))
	}
}

func TestFilteredPromptsExcludesBody(t *testing.T) {
	all := samplePromptTemplates()
	got := filteredPrompts(all, "bugs first")
	if len(got) != 0 {
		t.Fatalf("body match should be excluded, got %#v", got)
	}
}

func TestFormatPromptRowShape(t *testing.T) {
	row := formatPromptRow(samplePromptTemplates()[0], false, 200)
	for _, want := range []string{"review", "senior", "go", "replace", "prio=10", "review.base"} {
		if !strings.Contains(row, want) {
			t.Errorf("row missing %q: %s", want, row)
		}
	}
}

func TestFormatPromptRowHighlightsSelected(t *testing.T) {
	selected := formatPromptRow(samplePromptTemplates()[0], true, 200)
	unselected := formatPromptRow(samplePromptTemplates()[0], false, 200)
	if !strings.Contains(selected, "▶") {
		t.Fatalf("selected row missing arrow: %q", selected)
	}
	if strings.Contains(unselected, "▶") {
		t.Fatalf("unselected row should not carry arrow: %q", unselected)
	}
}

func TestFormatPromptRowFallsBackToQuestionMarkType(t *testing.T) {
	row := formatPromptRow(promptlib.Template{ID: "x"}, false, 200)
	if !strings.Contains(row, "?") {
		t.Fatalf("row with empty type should fall back to '?': %q", row)
	}
}

func TestFormatPromptPreviewRendersDescriptionAndBody(t *testing.T) {
	out := formatPromptPreview(samplePromptTemplates()[0], 120)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "description") {
		t.Fatalf("preview missing description header: %s", joined)
	}
	if !strings.Contains(joined, "Base review prompt") {
		t.Fatalf("preview missing description body: %s", joined)
	}
	if !strings.Contains(joined, "body") {
		t.Fatalf("preview missing body header: %s", joined)
	}
	if !strings.Contains(joined, "senior Go reviewer") {
		t.Fatalf("preview missing body line 1: %s", joined)
	}
	if !strings.Contains(joined, "Call out bugs first") {
		t.Fatalf("preview missing body line 2: %s", joined)
	}
}

func TestFormatPromptPreviewClipsLongBody(t *testing.T) {
	t2 := samplePromptTemplates()[0]
	t2.Body = strings.Repeat("x", promptsPreviewChars+500)
	out := formatPromptPreview(t2, 200)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "…") {
		t.Fatalf("clipped body should end with ellipsis: %q", joined[len(joined)-20:])
	}
}

func TestWrapPromptLinesSplitsToWidth(t *testing.T) {
	out := wrapPromptLines("alpha bravo charlie delta echo", 20)
	if len(out) == 0 {
		t.Fatalf("want at least one wrapped line")
	}
	for _, line := range out {
		if len(line) > 20 {
			t.Fatalf("line exceeds width-6 hint: %q (len=%d)", line, len(line))
		}
	}
}

func TestRenderPromptsViewEmptyState(t *testing.T) {
	m := newPromptsTestModel()
	out := m.renderPromptsView(80)
	if !strings.Contains(out, "Prompts") {
		t.Fatalf("header missing: %s", out)
	}
	if !strings.Contains(out, "No prompt templates loaded") {
		t.Fatalf("empty copy missing: %s", out)
	}
}

func TestRenderPromptsViewErrorBanner(t *testing.T) {
	m := newPromptsTestModel()
	m.promptsErr = "bad yaml"
	out := m.renderPromptsView(80)
	if !strings.Contains(out, "error · bad yaml") {
		t.Fatalf("error banner missing: %s", out)
	}
}

func TestRenderPromptsViewWithTemplates(t *testing.T) {
	m := newPromptsTestModel()
	m.promptsTemplates = samplePromptTemplates()
	out := m.renderPromptsView(140)
	if !strings.Contains(out, "3 shown · 3 loaded") {
		t.Fatalf("footer count wrong: %s", out)
	}
	if !strings.Contains(out, "review.base") {
		t.Fatalf("first row missing: %s", out)
	}
}

func TestPromptsScrollBindings(t *testing.T) {
	m := newPromptsTestModel()
	m.promptsTemplates = samplePromptTemplates()

	m2, _ := m.handlePromptsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m2.(Model)
	if m.promptsScroll != 1 {
		t.Fatalf("j should advance, got %d", m.promptsScroll)
	}
	m2, _ = m.handlePromptsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = m2.(Model)
	if m.promptsScroll != len(m.promptsTemplates)-1 {
		t.Fatalf("G should jump to last, got %d", m.promptsScroll)
	}
	m2, _ = m.handlePromptsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = m2.(Model)
	if m.promptsScroll != 0 {
		t.Fatalf("g should jump to top, got %d", m.promptsScroll)
	}
}

func TestPromptsSearchInputFlow(t *testing.T) {
	m := newPromptsTestModel()
	m.promptsTemplates = samplePromptTemplates()

	m2, _ := m.handlePromptsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = m2.(Model)
	if !m.promptsSearchActive {
		t.Fatalf("search mode should activate on /")
	}

	for _, r := range "explain" {
		m2, _ = m.handlePromptsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m2.(Model)
	}
	if m.promptsQuery != "explain" {
		t.Fatalf("want query=explain, got %q", m.promptsQuery)
	}

	m2, _ = m.handlePromptsKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m2.(Model)
	if m.promptsQuery != "explai" {
		t.Fatalf("backspace should trim, got %q", m.promptsQuery)
	}

	m2, _ = m.handlePromptsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(Model)
	if m.promptsSearchActive {
		t.Fatalf("enter should exit search mode")
	}
	if m.promptsScroll != 0 {
		t.Fatalf("enter should reset scroll, got %d", m.promptsScroll)
	}

	m2, _ = m.handlePromptsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = m2.(Model)
	if m.promptsQuery != "" {
		t.Fatalf("c should clear query, got %q", m.promptsQuery)
	}
}

func TestPromptsRefreshSetsLoading(t *testing.T) {
	m := newPromptsTestModel()
	m2, _ := m.handlePromptsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = m2.(Model)
	if !m.promptsLoading {
		t.Fatalf("r should set loading=true")
	}
	if m.promptsErr != "" {
		t.Fatalf("r should clear error, got %q", m.promptsErr)
	}
}

func TestPromptsEnterSetsPreviewID(t *testing.T) {
	m := newPromptsTestModel()
	m.promptsTemplates = samplePromptTemplates()
	m2, _ := m.handlePromptsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(Model)
	if m.promptsPreviewID != "review.base" {
		t.Fatalf("enter should stamp preview id of highlighted row, got %q", m.promptsPreviewID)
	}
}
