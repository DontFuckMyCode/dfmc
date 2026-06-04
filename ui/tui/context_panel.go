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
//
// Companion siblings (extracted to keep this file scannable):
//
//   - context_panel_blocks.go renderContextBudgetBlock /
//                             renderContextBreakdownBlock /
//                             renderContextActiveBlock + the small
//                             primitives they share (contextRatioBar,
//                             contextSeverityStyle, formatContextHintRow)
//   - context_panel_keys.go   handleContextKey + handleContextInputKey
//                             keyboard routers

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// contextTopBanner — title + state chip. EMPTY (no preview), TYPING,
// READY (preview computed), ERROR.
func (m Model) contextTopBanner(width int) string {
	title := titleStyle.Bold(true).Render("⚖ CONTEXT")
	chipText, chipStyle := " EMPTY ", subtleStyle
	switch {
	case m.contextPanel.err != "":
		chipText, chipStyle = " ERROR ", warnStyle
	case m.contextPanel.inputActive:
		chipText, chipStyle = " TYPING ", infoStyle
	case m.contextPanel.preview != nil:
		chipText, chipStyle = " READY ", okStyle
	}
	chip := chipStyle.Render(chipText)
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chip)-4, 1)
	return title + strings.Repeat(" ", gap) + chip
}

func (m Model) renderContextView(width int) string {
	// Route to Context Manager sub-view when active.
	if m.contextPanel.manager.active {
		out := m.renderContextManagerView(width, 0)
		if m.actionMenu.open && m.actionMenu.owner == "CtxMgr" {
			out += "\n\n" + m.renderActionMenu(width)
		}
		return out
	}
	out := m.renderContextViewInner(width)
	if m.actionMenu.open && m.actionMenu.owner == "Context" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

func (m Model) renderContextViewInner(width int) string {
	width = clampInt(width, 24, 1000)
	banner := m.contextTopBanner(width)
	// Context panel uses `e` (edit query) instead of `/` (search) — the
	// query is a context-preview input, not a row filter. panelIdleHint
	// would mis-claim `/ search` so we hand-roll the hint.
	hint := subtleStyle.Render("↑↓ scroll · e edit query · enter preview · m manager · c clear · → action menu")

	queryLine := subtleStyle.Render("query ")
	if strings.TrimSpace(m.contextPanel.query) != "" {
		queryLine += boldStyle.Render(m.contextPanel.query)
	} else {
		queryLine += subtleStyle.Render("(none — press e to enter a query)")
	}
	if m.contextPanel.inputActive {
		queryLine += subtleStyle.Render("  · typing, enter to preview")
	}

	lines := []string{banner, queryLine, hint, renderDivider(width - 2)}
	lines = append(lines, m.renderContextCockpitBlock(width)...)

	if m.contextPanel.err != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.contextPanel.err))
		return strings.Join(lines, "\n")
	}

	if m.contextPanel.preview == nil {
		lines = append(lines, "",
			subtleStyle.Render("No context preview yet."),
			subtleStyle.Render("This panel shows how the budgeter would rank and compress project files before an Ask — every token justified."),
			subtleStyle.Render("Press e to type a query, then enter to preview; m opens the context manager."),
		)
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "", subtleStyle.Render("budget"))
	lines = append(lines, renderContextBudgetBlock(*m.contextPanel.preview, width)...)

	if m.contextPanel.breakdown != nil {
		lines = append(lines, "", subtleStyle.Render("context breakdown"))
		lines = append(lines, renderContextBreakdownBlock(*m.contextPanel.breakdown, width)...)
	}

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

func renderContextPanelLines(lines []string, scroll, maxLines int) string {
	if maxLines <= 0 || len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	fixed := 4
	if fixed > len(lines) {
		fixed = len(lines)
	}
	bodySlots := maxLines - fixed
	if bodySlots <= 0 {
		return strings.Join(lines[:maxLines], "\n")
	}
	body := lines[fixed:]
	maxScroll := len(body) - bodySlots
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll = clampInt(scroll, 0, maxScroll)
	end := scroll + bodySlots
	if end > len(body) {
		end = len(body)
	}
	out := append([]string{}, lines[:fixed]...)
	out = append(out, body[scroll:end]...)
	if end < len(body) && len(out) > fixed {
		out[len(out)-1] = subtleStyle.Render(fmt.Sprintf("… more (%d lines), use pgdn/down", len(body)-end))
	}
	return strings.Join(out, "\n")
}

func (m Model) renderContextViewSized(width, height int) string {
	width = clampInt(width, 24, 1000)
	// Route to Context Manager sub-view when active.
	if m.contextPanel.manager.active {
		out := m.renderContextManagerViewSized(width, height)
		if m.actionMenu.open && m.actionMenu.owner == "CtxMgr" {
			out += "\n\n" + m.renderActionMenu(width)
		}
		return out
	}
	if !m.contextPanel.showActive {
		return renderContextPanelLines(strings.Split(m.renderContextView(width), "\n"), m.contextPanel.scroll, height)
	}

	header := sectionHeader("CTX", "Context")
	// Context panel uses `e` (edit query) instead of `/` (search) — the
	// query is a context-preview input, not a row filter. panelIdleHint
	// would mis-claim `/ search` so we hand-roll the hint.
	hint := subtleStyle.Render("↑↓ scroll · e edit query · enter preview · m manager · c clear · → action menu")
	queryLine := subtleStyle.Render("query: ")
	if strings.TrimSpace(m.contextPanel.query) != "" {
		queryLine += m.contextPanel.query
	} else {
		queryLine += subtleStyle.Render("(active context from last LLM request)")
	}
	lines := []string{header, hint, queryLine, renderDivider(width - 2)}

	if m.contextPanel.err != "" {
		lines = append(lines, "", warnStyle.Render("error - "+m.contextPanel.err))
		return renderContextPanelLines(lines, m.contextPanel.scroll, height)
	}

	active := m.contextPanel.active
	if m.eng != nil {
		debug := m.eng.ActiveContextDebug()
		active = &debug
	}
	lines = append(lines, "", subtleStyle.Render("active context debug"))
	if active == nil || (strings.TrimSpace(active.Query) == "" && len(active.Files) == 0 && len(active.Reasons) == 0) {
		lines = append(lines,
			"",
			warnStyle.Render("  no active context captured yet"),
			subtleStyle.Render("  Run a chat request first; this view shows the exact chunks from the last LLM request."),
		)
		return renderContextPanelLines(lines, m.contextPanel.scroll, height)
	}
	lines = append(lines, renderContextActiveBlock(*active, width)...)
	return renderContextPanelLines(lines, m.contextPanel.scroll, height)
}

// runContextPreview recomputes the budget info, hints, and real-time
// context breakdown for the current query. Pure (no goroutines) —
// all called functions read only config/state, so no tea.Cmd needed.
func (m Model) runContextPreview() Model {
	q := strings.TrimSpace(m.contextPanel.query)
	if q == "" {
		m.contextPanel.preview = nil
		m.contextPanel.breakdown = nil
		m.contextPanel.hints = nil
		m.contextPanel.err = "query is empty"
		return m
	}
	if m.eng == nil {
		m.contextPanel.preview = nil
		m.contextPanel.breakdown = nil
		m.contextPanel.hints = nil
		m.contextPanel.err = "engine not ready — another dfmc process may hold the store lock (try `dfmc doctor`)"
		return m
	}
	m.contextPanel.err = ""
	preview := func() *engine.ContextBudgetInfo {
		if m.eng == nil {
			return nil
		}
		info := m.eng.ContextBudgetPreview(q)
		return &info
	}()
	m.contextPanel.preview = preview
	m.contextPanel.breakdown = new(engine.ContextBreakdown)
	*m.contextPanel.breakdown = m.eng.ContextBreakdown(q)
	m.contextPanel.hints = m.eng.ContextRecommendations(q)
	m.contextPanel.showActive = false
	m.contextPanel.scroll = 0
	return m
}

func (m Model) loadActiveContextDebug() Model {
	m.contextPanel.showActive = true
	m.contextPanel.scroll = 0
	m.contextPanel.err = ""
	if m.eng == nil {
		m.contextPanel.active = nil
		m.contextPanel.err = "engine not ready - active context is unavailable"
		return m
	}
	active := m.eng.ActiveContextDebug()
	m.contextPanel.active = &active
	return m
}

// openContextActionMenu — arrow-driven discovery for the Context tab.
func (m Model) openContextActionMenu() Model {
	actions := []panelAction{
		{Label: "Edit query (opens text input)", Accel: "e",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.contextPanel.inputActive = true
				return m, nil
			}},
		{Label: "Re-run preview with current query",
			Handler: func(m Model) (Model, tea.Cmd) {
				if strings.TrimSpace(m.contextPanel.query) != "" {
					m = m.runContextPreview()
				}
				return m, nil
			}},
		{Label: "Show active context (last engine snapshot)", Accel: "a",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.loadActiveContextDebug(), nil
			}},
		{Label: "Clear query and preview", Accel: "c",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.contextPanel.query = ""
				m.contextPanel.preview = nil
				m.contextPanel.breakdown = nil
				m.contextPanel.hints = nil
				m.contextPanel.active = nil
				m.contextPanel.showActive = false
				m.contextPanel.scroll = 0
				m.contextPanel.err = ""
				return m, nil
			}},
		{Label: "Context Manager (select & delete messages)", Accel: "m",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.activateContextManager(), nil
			}},
	}
	return m.openActionMenu("Context", "Context actions", actions)
}
