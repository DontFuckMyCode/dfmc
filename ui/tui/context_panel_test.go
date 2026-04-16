package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func newContextTestModel() Model {
	return Model{
		tabs:      []string{"Chat", "Status", "Files", "Patch", "Setup", "Tools", "Activity", "Memory", "CodeMap", "Conversations", "Prompts", "Security", "Plans", "Context"},
		activeTab: 13,
	}
}

func sampleContextBudgetInfo() engine.ContextBudgetInfo {
	return engine.ContextBudgetInfo{
		Provider:               "anthropic",
		Model:                  "claude-opus-4",
		ProviderMaxContext:     200000,
		Task:                   "review",
		ExplicitFileMentions:   2,
		TaskTotalScale:         1.25,
		TaskFileScale:          1.0,
		TaskPerFileScale:       1.1,
		ContextAvailableTokens: 150000,
		ReserveTotalTokens:     50000,
		ReservePromptTokens:    2000,
		ReserveHistoryTokens:   8000,
		ReserveResponseTokens:  30000,
		ReserveToolTokens:      10000,
		MaxFiles:               20,
		MaxTokensTotal:         120000,
		MaxTokensPerFile:       8000,
		MaxHistoryTokens:       8000,
		Compression:            "sectional",
		IncludeTests:           true,
		IncludeDocs:            false,
	}
}

func TestContextRatioBarFillsProportional(t *testing.T) {
	cases := []struct {
		used, total int
		wantFilled  int
	}{
		{0, 100, 0},
		{50, 100, 5},
		{100, 100, 10},
		{200, 100, 10}, // clamps
		{-5, 100, 0},   // clamps
	}
	for _, c := range cases {
		bar := contextRatioBar(c.used, c.total)
		got := strings.Count(bar, "█")
		if got != c.wantFilled {
			t.Errorf("contextRatioBar(%d,%d) filled=%d want=%d bar=%q", c.used, c.total, got, c.wantFilled, bar)
		}
	}
}

func TestContextRatioBarZeroTotal(t *testing.T) {
	bar := contextRatioBar(5, 0)
	if strings.Count(bar, "█") != 0 {
		t.Fatalf("zero total should yield empty bar, got %q", bar)
	}
	if len([]rune(bar)) != 10 {
		t.Fatalf("bar width should stay 10 runes, got %d", len([]rune(bar)))
	}
}

func TestContextSeverityStyleVariants(t *testing.T) {
	cases := []struct {
		sev  string
		want string
	}{
		{"critical", "CRITICAL"},
		{"error", "ERROR"},
		{"warn", "WARN"},
		{"warning", "WARNING"},
		{"info", "INFO"},
		{"", "NOTE"},
		{"hint", "HINT"},
	}
	for _, c := range cases {
		got := contextSeverityStyle(c.sev)
		if !strings.Contains(got, c.want) {
			t.Errorf("contextSeverityStyle(%q) missing %q, got %q", c.sev, c.want, got)
		}
	}
}

func TestFormatContextHintRowContainsFields(t *testing.T) {
	h := engine.ContextRecommendation{Severity: "warn", Code: "BUDGET_TIGHT", Message: "reserve eats 60% of the window"}
	row := formatContextHintRow(h, 200)
	for _, want := range []string{"WARN", "BUDGET_TIGHT", "reserve eats"} {
		if !strings.Contains(row, want) {
			t.Errorf("row missing %q: %s", want, row)
		}
	}
}

func TestRenderContextBudgetBlockSections(t *testing.T) {
	info := sampleContextBudgetInfo()
	lines := renderContextBudgetBlock(info, 200)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"anthropic", "claude-opus-4", "max_context=200000",
		"task: review", "2 [[file:]]",
		"reserve", "50000/200000",
		"prompt=2000", "history=8000", "response=30000", "tool=10000",
		"available for context: 150000",
		"files=20", "total=120000", "per_file=8000", "history=8000",
		"compression=sectional", "tests=on", "docs=off",
		"task scale", "total=1.25",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("budget block missing %q:\n%s", want, joined)
		}
	}
}

func TestRenderContextViewEmptyState(t *testing.T) {
	m := newContextTestModel()
	out := m.renderContextView(100)
	if !strings.Contains(out, "Context") {
		t.Fatalf("header missing: %s", out)
	}
	if !strings.Contains(out, "press e to enter a query") {
		t.Fatalf("empty query hint missing: %s", out)
	}
	if !strings.Contains(out, "offline against current config") {
		t.Fatalf("body copy missing: %s", out)
	}
}

func TestRenderContextViewWithPreview(t *testing.T) {
	m := newContextTestModel()
	m.contextQuery = "review the router"
	info := sampleContextBudgetInfo()
	m.contextPreview = &info
	m.contextHints = []engine.ContextRecommendation{
		{Severity: "warn", Code: "TIGHT", Message: "reserve is 25% of window"},
	}
	out := m.renderContextView(160)
	for _, want := range []string{
		"review the router",
		"budget",
		"anthropic",
		"hints",
		"WARN",
		"TIGHT",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestRenderContextViewNoHintsCopy(t *testing.T) {
	m := newContextTestModel()
	m.contextQuery = "review"
	info := sampleContextBudgetInfo()
	m.contextPreview = &info
	out := m.renderContextView(140)
	if !strings.Contains(out, "hints: none") {
		t.Fatalf("no-hints copy missing: %s", out)
	}
}

func TestRenderContextViewErrorBanner(t *testing.T) {
	m := newContextTestModel()
	m.contextErr = "engine not ready"
	out := m.renderContextView(80)
	if !strings.Contains(out, "error · engine not ready") {
		t.Fatalf("error banner missing: %s", out)
	}
}

func TestRunContextPreviewRejectsEmpty(t *testing.T) {
	m := newContextTestModel()
	m.contextQuery = "   "
	m = m.runContextPreview()
	if m.contextPreview != nil {
		t.Fatalf("empty query should not produce a preview")
	}
	if m.contextErr == "" {
		t.Fatalf("empty query should set an error")
	}
}

func TestRunContextPreviewRequiresEngine(t *testing.T) {
	m := newContextTestModel()
	m.contextQuery = "something"
	m.eng = nil
	m = m.runContextPreview()
	if m.contextPreview != nil {
		t.Fatalf("nil engine should not produce a preview")
	}
	if !strings.Contains(m.contextErr, "engine") {
		t.Fatalf("nil engine should set engine-related error, got %q", m.contextErr)
	}
}

func TestContextEditFlowCommitsOnEnter(t *testing.T) {
	m := newContextTestModel()

	m2, _ := m.handleContextKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = m2.(Model)
	if !m.contextInputActive {
		t.Fatalf("e should activate input mode")
	}

	for _, r := range "hello" {
		m2, _ = m.handleContextKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m2.(Model)
	}
	if m.contextQuery != "hello" {
		t.Fatalf("typed query mismatch: %q", m.contextQuery)
	}

	m2, _ = m.handleContextKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(Model)
	if m.contextInputActive {
		t.Fatalf("enter should exit input mode")
	}
	// eng is nil — preview stays nil, error set instead.
	if m.contextErr == "" {
		t.Fatalf("nil engine path should surface an error")
	}
}

func TestContextEditEscCancels(t *testing.T) {
	m := newContextTestModel()
	m2, _ := m.handleContextKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = m2.(Model)
	m2, _ = m.handleContextKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(Model)
	if m.contextInputActive {
		t.Fatalf("esc should exit input mode")
	}
	if m.contextPreview != nil {
		t.Fatalf("esc should not trigger a preview")
	}
}

func TestContextEditBackspaceTrims(t *testing.T) {
	m := newContextTestModel()
	m.contextQuery = "abc"
	m.contextInputActive = true
	m2, _ := m.handleContextKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m2.(Model)
	if m.contextQuery != "ab" {
		t.Fatalf("backspace should trim last rune, got %q", m.contextQuery)
	}
}

func TestContextClearResetsAll(t *testing.T) {
	m := newContextTestModel()
	m.contextQuery = "something"
	info := sampleContextBudgetInfo()
	m.contextPreview = &info
	m.contextHints = []engine.ContextRecommendation{{Severity: "info", Code: "X", Message: "Y"}}
	m.contextErr = "stale"
	m2, _ := m.handleContextKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = m2.(Model)
	if m.contextQuery != "" {
		t.Fatalf("c should clear query, got %q", m.contextQuery)
	}
	if m.contextPreview != nil {
		t.Fatalf("c should drop the preview")
	}
	if m.contextHints != nil {
		t.Fatalf("c should drop hints")
	}
	if m.contextErr != "" {
		t.Fatalf("c should clear error")
	}
}
