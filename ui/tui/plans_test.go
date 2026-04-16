package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/planning"
)

func newPlansTestModel() Model {
	return Model{
		tabs:      []string{"Chat", "Status", "Files", "Patch", "Setup", "Tools", "Activity", "Memory", "CodeMap", "Conversations", "Prompts", "Security", "Plans"},
		activeTab: 12,
	}
}

func TestPlansConfidenceLabel(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.9, "strong"},
		{0.7, "strong"},
		{0.69, "weak"},
		{0.4, "weak"},
		{0.3, "none"},
		{0.0, "none"},
	}
	for _, c := range cases {
		if got := plansConfidenceLabel(c.in); got != c.want {
			t.Errorf("plansConfidenceLabel(%.2f) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPlansConfidenceBarFillsProportional(t *testing.T) {
	cases := []struct {
		in    float64
		want  int // expected filled blocks
	}{
		{0.0, 0},
		{0.25, 2},
		{0.5, 5},
		{0.85, 8},
		{1.0, 10},
	}
	for _, c := range cases {
		bar := plansConfidenceBar(c.in)
		got := strings.Count(bar, "█")
		if got != c.want {
			t.Errorf("plansConfidenceBar(%.2f) filled=%d want=%d (bar=%q)", c.in, got, c.want, bar)
		}
	}
}

func TestPlansConfidenceBarClampsOutOfRange(t *testing.T) {
	if strings.Count(plansConfidenceBar(-0.5), "█") != 0 {
		t.Fatalf("negative confidence should clamp to empty bar")
	}
	if strings.Count(plansConfidenceBar(1.5), "█") != 10 {
		t.Fatalf("confidence > 1 should clamp to full bar")
	}
}

func TestFormatPlansSubtaskRowShape(t *testing.T) {
	s := planning.Subtask{Title: "survey the router", Hint: "numbered-list", Description: "survey the router"}
	row := formatPlansSubtaskRow(0, s, false, 200)
	for _, want := range []string{"1.", "numbered-list", "survey the router"} {
		if !strings.Contains(row, want) {
			t.Errorf("row missing %q: %s", want, row)
		}
	}
}

func TestFormatPlansSubtaskRowHighlightsSelected(t *testing.T) {
	s := planning.Subtask{Title: "x", Hint: "single"}
	sel := formatPlansSubtaskRow(0, s, true, 80)
	uns := formatPlansSubtaskRow(0, s, false, 80)
	if !strings.Contains(sel, "▶") {
		t.Fatalf("selected row should carry arrow: %q", sel)
	}
	if strings.Contains(uns, "▶") {
		t.Fatalf("unselected row should not carry arrow: %q", uns)
	}
}

func TestRenderPlansViewEmptyState(t *testing.T) {
	m := newPlansTestModel()
	out := m.renderPlansView(100)
	if !strings.Contains(out, "Plans") {
		t.Fatalf("header missing: %s", out)
	}
	if !strings.Contains(out, "press e to enter a task") {
		t.Fatalf("empty query hint missing: %s", out)
	}
	if !strings.Contains(out, "Offline task decomposer") {
		t.Fatalf("body copy missing: %s", out)
	}
}

func TestRenderPlansViewNumberedSplit(t *testing.T) {
	m := newPlansTestModel()
	m.plansQuery = "do three things: 1) survey the tool registry 2) map the provider router 3) document context manager"
	m = m.runPlansSplit()
	out := m.renderPlansView(160)
	if m.plansPlan == nil || len(m.plansPlan.Subtasks) != 3 {
		t.Fatalf("expected 3 subtasks, got plan=%+v", m.plansPlan)
	}
	if !strings.Contains(out, "3 subtasks") {
		t.Fatalf("summary line missing: %s", out)
	}
	if !strings.Contains(out, "parallel") {
		t.Fatalf("parallel verdict missing: %s", out)
	}
	if !strings.Contains(out, "numbered-list") {
		t.Fatalf("hint chip missing: %s", out)
	}
	if !strings.Contains(out, "Strong parallel split") {
		t.Fatalf("parallel recommendation banner missing: %s", out)
	}
}

func TestRenderPlansViewStagesAreSerial(t *testing.T) {
	m := newPlansTestModel()
	m.plansQuery = "first run the tests, then inspect the failures, then write the fix"
	m = m.runPlansSplit()
	out := m.renderPlansView(140)
	if !strings.Contains(out, "serial") {
		t.Fatalf("serial verdict missing: %s", out)
	}
	if !strings.Contains(out, "stage") {
		t.Fatalf("stage hint missing: %s", out)
	}
	if !strings.Contains(out, "Serial plan") {
		t.Fatalf("serial banner missing: %s", out)
	}
}

func TestRenderPlansViewSingleTaskBanner(t *testing.T) {
	m := newPlansTestModel()
	m.plansQuery = "fix the parser"
	m = m.runPlansSplit()
	out := m.renderPlansView(100)
	if !strings.Contains(out, "single") {
		t.Fatalf("single verdict missing: %s", out)
	}
}

func TestRunPlansSplitRejectsEmpty(t *testing.T) {
	m := newPlansTestModel()
	m.plansQuery = "   "
	m = m.runPlansSplit()
	if m.plansPlan != nil {
		t.Fatalf("empty query should not produce a plan")
	}
	if m.plansErr == "" {
		t.Fatalf("empty query should set an error")
	}
}

func TestPlansEditFlowCommitsOnEnter(t *testing.T) {
	m := newPlansTestModel()

	m2, _ := m.handlePlansKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = m2.(Model)
	if !m.plansInputActive {
		t.Fatalf("e should activate input mode")
	}

	for _, r := range "fix A" {
		m2, _ = m.handlePlansKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m2.(Model)
	}
	if m.plansQuery != "fix A" {
		t.Fatalf("typed query mismatch: %q", m.plansQuery)
	}

	m2, _ = m.handlePlansKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(Model)
	if m.plansInputActive {
		t.Fatalf("enter should exit input mode")
	}
	if m.plansPlan == nil {
		t.Fatalf("enter should run the split")
	}
}

func TestPlansEditEscCancels(t *testing.T) {
	m := newPlansTestModel()
	m2, _ := m.handlePlansKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = m2.(Model)
	m2, _ = m.handlePlansKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(Model)
	if m.plansInputActive {
		t.Fatalf("esc should exit input mode")
	}
	if m.plansPlan != nil {
		t.Fatalf("esc should not trigger a split")
	}
}

func TestPlansEditBackspaceTrims(t *testing.T) {
	m := newPlansTestModel()
	m.plansQuery = "abc"
	m.plansInputActive = true
	m2, _ := m.handlePlansKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m2.(Model)
	if m.plansQuery != "ab" {
		t.Fatalf("backspace should trim last rune, got %q", m.plansQuery)
	}
}

func TestPlansScrollBindings(t *testing.T) {
	m := newPlansTestModel()
	m.plansQuery = "survey engine.go, and map the router, and document the manager"
	m = m.runPlansSplit()
	if m.plansPlan == nil || len(m.plansPlan.Subtasks) < 2 {
		t.Fatalf("sanity: multi-conjunction should split, got %+v", m.plansPlan)
	}

	m2, _ := m.handlePlansKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m2.(Model)
	if m.plansScroll != 1 {
		t.Fatalf("j should advance scroll, got %d", m.plansScroll)
	}
	m2, _ = m.handlePlansKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = m2.(Model)
	if m.plansScroll != len(m.plansPlan.Subtasks)-1 {
		t.Fatalf("G should jump to last, got %d", m.plansScroll)
	}
	m2, _ = m.handlePlansKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = m2.(Model)
	if m.plansScroll != 0 {
		t.Fatalf("g should jump to top, got %d", m.plansScroll)
	}
}

func TestPlansClearResetsAll(t *testing.T) {
	m := newPlansTestModel()
	m.plansQuery = "something"
	m = m.runPlansSplit()
	m.plansScroll = 0
	m2, _ := m.handlePlansKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = m2.(Model)
	if m.plansQuery != "" {
		t.Fatalf("c should clear query, got %q", m.plansQuery)
	}
	if m.plansPlan != nil {
		t.Fatalf("c should drop the plan")
	}
	if m.plansErr != "" {
		t.Fatalf("c should clear the error")
	}
}
