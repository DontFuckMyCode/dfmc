package tui

// context_panel.go — the Context panel exposes the budgeting decisions
// internal/context.Manager would make for a given query, before an Ask
// is ever sent. It surfaces the "every token is justified" principle:
// the user sees the provider cap, the reserve breakdown, the file/
// per-file caps the task profile picks, and any hints the engine's
// ContextRecommendations layer surfaces.
//
// Shape: query string, cached ContextBudgetInfo + hints list, an
// edit-mode flag. Computation is offline — ContextBudgetPreview is a
// pure function over the engine's current config — so we recompute on
// every enter without a tea.Cmd round-trip.
//
// Named context_panel.go (not context.go) to avoid colliding with the
// Go stdlib package name in greppable ways.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// contextRatioBar renders a percentage as a 10-wide meter so the eye
// picks out "reserve eats 80% of the window" without reading numbers.
func contextRatioBar(used, total int) string {
	width := 10
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	filled := (used * width) / total
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// contextSeverityStyle picks the warn colour for error/warn hints and
// the subtle style for info/note.
func contextSeverityStyle(sev string) string {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "error", "critical", "warn", "warning":
		return warnStyle.Render(strings.ToUpper(sev))
	case "info":
		return accentStyle.Render("INFO")
	default:
		return subtleStyle.Render(strings.ToUpper(nonEmpty(sev, "note")))
	}
}

// formatContextHintRow shapes one hint line: `[WARN] code · message`.
func formatContextHintRow(h engine.ContextRecommendation, width int) string {
	tag := contextSeverityStyle(h.Severity)
	line := "  " + tag
	if h.Code != "" {
		line += "  " + subtleStyle.Render(h.Code)
	}
	if h.Message != "" {
		line += "  " + h.Message
	}
	if width > 0 {
		line = truncateSingleLine(line, width)
	}
	return line
}

// renderContextBudgetBlock renders the provider/model line, the reserve
// vs. available bar, and the budget-caps block. Pulled into its own
// helper so the tests can exercise each section without rendering the
// whole view.
func renderContextBudgetBlock(info engine.ContextBudgetInfo, width int) []string {
	out := []string{}
	if info.Provider != "" || info.Model != "" {
		head := accentStyle.Render(nonEmpty(info.Provider, "?"))
		if info.Model != "" {
			head += subtleStyle.Render("/"+info.Model)
		}
		head += subtleStyle.Render(fmt.Sprintf("  max_context=%d", info.ProviderMaxContext))
		out = append(out, "  "+head)
	}
	if info.Task != "" {
		taskLine := subtleStyle.Render("task: ") + info.Task
		if info.ExplicitFileMentions > 0 {
			taskLine += subtleStyle.Render(fmt.Sprintf("  · %d [[file:]] markers", info.ExplicitFileMentions))
		}
		out = append(out, "  "+taskLine)
	}

	totalBudget := info.ProviderMaxContext
	if totalBudget <= 0 {
		totalBudget = info.ContextAvailableTokens + info.ReserveTotalTokens
	}
	bar := contextRatioBar(info.ReserveTotalTokens, totalBudget)
	reserveLine := subtleStyle.Render("reserve ") + bar + subtleStyle.Render(
		fmt.Sprintf(" %d/%d tokens (prompt=%d history=%d response=%d tool=%d)",
			info.ReserveTotalTokens, totalBudget,
			info.ReservePromptTokens, info.ReserveHistoryTokens,
			info.ReserveResponseTokens, info.ReserveToolTokens,
		),
	)
	out = append(out, "  "+reserveLine)

	availLine := subtleStyle.Render(fmt.Sprintf(
		"available for context: %d tokens",
		info.ContextAvailableTokens,
	))
	out = append(out, "  "+availLine)

	caps := fmt.Sprintf(
		"caps: files=%d  total=%d  per_file=%d  history=%d",
		info.MaxFiles, info.MaxTokensTotal, info.MaxTokensPerFile, info.MaxHistoryTokens,
	)
	out = append(out, "  "+subtleStyle.Render(caps))

	modes := []string{}
	if info.Compression != "" {
		modes = append(modes, "compression="+info.Compression)
	}
	if info.IncludeTests {
		modes = append(modes, "tests=on")
	} else {
		modes = append(modes, "tests=off")
	}
	if info.IncludeDocs {
		modes = append(modes, "docs=on")
	} else {
		modes = append(modes, "docs=off")
	}
	out = append(out, "  "+subtleStyle.Render("modes: "+strings.Join(modes, " · ")))

	if info.TaskTotalScale > 0 || info.TaskFileScale > 0 || info.TaskPerFileScale > 0 {
		scales := fmt.Sprintf(
			"task scale: total=%.2f files=%.2f per_file=%.2f",
			info.TaskTotalScale, info.TaskFileScale, info.TaskPerFileScale,
		)
		out = append(out, "  "+subtleStyle.Render(scales))
	}

	_ = width
	return out
}

func (m Model) renderContextView(width int) string {
	width = clampInt(width, 24, 1000)
	hint := subtleStyle.Render("e edit · enter preview · esc cancel edit · c clear")
	header := sectionHeader("⚖", "Context")

	queryLine := subtleStyle.Render("query: ")
	if strings.TrimSpace(m.contextPanel.query) != "" {
		queryLine += m.contextPanel.query
	} else {
		queryLine += subtleStyle.Render("(none — press e to enter a query)")
	}
	if m.contextPanel.inputActive {
		queryLine += subtleStyle.Render("  · typing, enter to preview")
	}

	lines := []string{header, hint, queryLine, renderDivider(width - 2)}

	if m.contextPanel.err != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.contextPanel.err))
		return strings.Join(lines, "\n")
	}

	if m.contextPanel.preview == nil {
		lines = append(lines,
			"",
			subtleStyle.Render("Shows how DFMC would budget context for a query."),
			subtleStyle.Render("Provider cap → reserve (prompt+history+response+tool) → available for files."),
			subtleStyle.Render("Type a query with e and press enter — runs offline against current config."),
		)
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "", subtleStyle.Render("budget"))
	lines = append(lines, renderContextBudgetBlock(*m.contextPanel.preview, width)...)

	if len(m.contextPanel.hints) > 0 {
		lines = append(lines, "", subtleStyle.Render("hints"))
		for _, h := range m.contextPanel.hints {
			lines = append(lines, formatContextHintRow(h, width-2))
		}
	} else {
		lines = append(lines, "", subtleStyle.Render("hints: none — current config looks healthy for this query."))
	}

	return strings.Join(lines, "\n")
}

// runContextPreview recomputes the budget info + hints for the current
// query. Pure (no goroutines) — engine.ContextBudgetPreview only reads
// config, so we don't need a tea.Cmd.
func (m Model) runContextPreview() Model {
	q := strings.TrimSpace(m.contextPanel.query)
	if q == "" {
		m.contextPanel.preview = nil
		m.contextPanel.hints = nil
		m.contextPanel.err = "query is empty"
		return m
	}
	if m.eng == nil {
		m.contextPanel.preview = nil
		m.contextPanel.hints = nil
		m.contextPanel.err = "engine not ready (degraded startup)"
		return m
	}
	m.contextPanel.err = ""
	preview := m.eng.ContextBudgetPreview(q)
	m.contextPanel.preview = &preview
	m.contextPanel.hints = m.eng.ContextRecommendations(q)
	return m
}

func (m Model) handleContextKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.contextPanel.inputActive {
		return m.handleContextInputKey(msg)
	}
	switch msg.String() {
	case "e":
		m.contextPanel.inputActive = true
		return m, nil
	case "enter":
		if strings.TrimSpace(m.contextPanel.query) != "" {
			m = m.runContextPreview()
		}
		return m, nil
	case "c":
		m.contextPanel.query = ""
		m.contextPanel.preview = nil
		m.contextPanel.hints = nil
		m.contextPanel.err = ""
		return m, nil
	}
	return m, nil
}

func (m Model) handleContextInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.contextPanel.inputActive = false
		m = m.runContextPreview()
		return m, nil
	case tea.KeyEsc:
		m.contextPanel.inputActive = false
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.contextPanel.query); len(r) > 0 {
			m.contextPanel.query = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.contextPanel.query += msg.String()
		return m, nil
	}
	return m, nil
}
