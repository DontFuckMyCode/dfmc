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
	"github.com/charmbracelet/lipgloss"

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
			head += subtleStyle.Render("/" + info.Model)
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
	if info.AutoIncludeFiles {
		modes = append(modes, "workspace_files=auto")
	} else {
		modes = append(modes, "workspace_files=explicit/tool")
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

// renderContextBreakdownBlock renders the real-time context breakdown
// as a visual bar chart + per-row breakdown. This is the "how full is
// my context window" view the user sees after entering a query.
func renderContextBreakdownBlock(bd engine.ContextBreakdown, width int) []string {
	out := []string{}

	// Provider / model / max line
	if bd.Provider != "" || bd.Model != "" {
		head := accentStyle.Render(nonEmpty(bd.Provider, "?"))
		if bd.Model != "" {
			head += subtleStyle.Render("/" + bd.Model)
		}
		head += subtleStyle.Render(fmt.Sprintf("  %dK ctx", bd.MaxContext/1000))
		out = append(out, "  "+head)
	}

	// Main bar: used vs total
	totalUsed := bd.UsedTotal
	bar := contextRatioBar(totalUsed, bd.MaxContext)
	if bd.MaxContext > 0 {
		pct := float64(totalUsed) / float64(bd.MaxContext) * 100
		out = append(out, "  "+bar+subtleStyle.Render(fmt.Sprintf("  %d%%  ·  %d / %d",
			int(pct), totalUsed/1000, bd.MaxContext/1000)))
	} else {
		out = append(out, "  "+bar+subtleStyle.Render("  ??%%"))
	}

	// Per-bucket rows
	rows := []struct {
		label string
		value int
		pct   float64
	}{
		{"system prompt", bd.SystemPrompt, bd.SystemPromptPct},
		{"history", bd.History, bd.HistoryPct},
		{"file context", bd.ContextChunks, bd.ContextChunksPct},
		{"tool reserve", bd.ToolReserve, 0},
		{"response", bd.Response, bd.ResponsePct},
	}
	for _, row := range rows {
		bar := contextRatioBar(int(float64(bd.MaxContext)*row.pct), bd.MaxContext)
		if row.value == 0 && row.pct == 0 {
			bar = strings.Repeat("░", 10)
		}
		pctStr := fmt.Sprintf("%d%%", int(row.pct*100))
		line := fmt.Sprintf("  %-12s %5d  %s  %s",
			subtleStyle.Render(row.label), row.value/1000,
			bar, subtleStyle.Render(pctStr))
		out = append(out, line)
	}

	// Files in context
	if len(bd.FilesInContext) > 0 {
		out = append(out, "")
		out = append(out, "  "+subtleStyle.Render(fmt.Sprintf("files (%d):", len(bd.FilesInContext))))
		for _, f := range bd.FilesInContext {
			if len(out) > 30 { // safety cap to avoid runaway output
				remaining := len(bd.FilesInContext) - (len(out) - 31)
				if remaining > 0 {
					out = append(out, "  "+subtleStyle.Render(fmt.Sprintf("  ... +%d more", remaining)))
				}
				break
			}
			out = append(out, "   "+subtleStyle.Render("▸ "+f))
		}
	}

	// Compression + task footer
	footer := ""
	if bd.Compression != "" {
		footer += "compression: " + bd.Compression
	}
	if bd.Task != "" {
		if footer != "" {
			footer += " · "
		}
		footer += "task: " + bd.Task
	}
	if footer != "" {
		out = append(out, "  "+subtleStyle.Render(footer))
	}

	return out
}

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
	width = clampInt(width, 24, 1000)
	banner := m.contextTopBanner(width)
	hint := subtleStyle.Render("e edit · enter preview · esc cancel · c clear")

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

// renderContextActiveBlock shows the exact chunks from the last LLM request.
func renderContextActiveBlock(debug engine.ContextDebugStatus, width int) []string {
	out := []string{}
	meta := []string{}
	if debug.Provider != "" || debug.Model != "" {
		provider := nonEmpty(debug.Provider, "?")
		if debug.Model != "" {
			provider += "/" + debug.Model
		}
		meta = append(meta, provider)
	}
	if !debug.BuiltAt.IsZero() {
		meta = append(meta, "built="+debug.BuiltAt.Format("15:04:05"))
	}
	if debug.Task != "" {
		meta = append(meta, "task="+debug.Task)
	}
	if debug.ProviderMaxContext > 0 {
		meta = append(meta, fmt.Sprintf("window=%d", debug.ProviderMaxContext))
	}
	if debug.MaxTokensTotal > 0 {
		meta = append(meta, fmt.Sprintf("budget=%d", debug.MaxTokensTotal))
	}
	if debug.TokenCount > 0 || debug.FileCount > 0 {
		meta = append(meta, fmt.Sprintf("used=%d tok / %d files", debug.TokenCount, debug.FileCount))
	}
	if len(meta) > 0 {
		out = append(out, "  "+accentStyle.Render(strings.Join(meta, "  |  ")))
	}
	if strings.TrimSpace(debug.Query) != "" {
		out = append(out, "  "+subtleStyle.Render("query: ")+debug.Query)
	}
	if len(debug.Reasons) > 0 {
		out = append(out, "", "  "+subtleStyle.Render("why this context shape:"))
		for _, reason := range debug.Reasons {
			if strings.TrimSpace(reason) == "" {
				continue
			}
			out = append(out, "   "+subtleStyle.Render("- "+reason))
		}
	}
	if len(debug.Files) == 0 {
		out = append(out, "", warnStyle.Render("  no active context chunks captured yet"))
		return out
	}
	out = append(out, "", "  "+subtleStyle.Render("active chunks (exact content sent to the model):"))
	for i, file := range debug.Files {
		out = append(out, "")
		rangeLabel := ""
		if file.LineStart > 0 || file.LineEnd > 0 {
			rangeLabel = fmt.Sprintf(":%d-%d", file.LineStart, file.LineEnd)
		}
		header := fmt.Sprintf("[%02d] %s%s", i+1, nonEmpty(file.Path, "(unknown)"), rangeLabel)
		stats := []string{}
		if file.Language != "" {
			stats = append(stats, "lang="+file.Language)
		}
		if file.TokenCount > 0 {
			stats = append(stats, fmt.Sprintf("tok=%d", file.TokenCount))
		}
		if file.Score > 0 {
			stats = append(stats, fmt.Sprintf("score=%.2f", file.Score))
		}
		if file.Compression != "" {
			stats = append(stats, "compression="+file.Compression)
		}
		if file.Source != "" {
			stats = append(stats, "source="+file.Source)
		}
		if len(stats) > 0 {
			header += "  " + subtleStyle.Render(strings.Join(stats, "  "))
		}
		out = append(out, "  "+accentStyle.Render(header))
		if strings.TrimSpace(file.Reason) != "" {
			out = append(out, "  "+subtleStyle.Render("reason: ")+file.Reason)
		}
		content := strings.ReplaceAll(file.Content, "\r\n", "\n")
		if strings.TrimSpace(content) == "" {
			out = append(out, "  "+warnStyle.Render("(chunk content is empty)"))
			continue
		}
		for lineIdx, line := range strings.Split(content, "\n") {
			if file.LineStart > 0 {
				out = append(out, fmt.Sprintf("  %5d | %s", file.LineStart+lineIdx, line))
			} else {
				out = append(out, "        | "+line)
			}
		}
	}
	_ = width
	return out
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
		out[len(out)-1] = subtleStyle.Render(fmt.Sprintf("... more (%d lines), use pgdn/down", len(body)-end))
	}
	return strings.Join(out, "\n")
}

func (m Model) renderContextViewSized(width, height int) string {
	width = clampInt(width, 24, 1000)
	if !m.contextPanel.showActive {
		return renderContextPanelLines(strings.Split(m.renderContextView(width), "\n"), m.contextPanel.scroll, height)
	}

	header := sectionHeader("CTX", "Context")
	hint := subtleStyle.Render("active full context | e preview query | enter preview | c clear | up/down/pg scroll")
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

func (m Model) handleContextKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.contextPanel.inputActive {
		return m.handleContextInputKey(msg)
	}
	switch msg.String() {
	case "a", "f":
		m = m.loadActiveContextDebug()
		return m, nil
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
		m.contextPanel.breakdown = nil
		m.contextPanel.hints = nil
		m.contextPanel.active = nil
		m.contextPanel.showActive = false
		m.contextPanel.scroll = 0
		m.contextPanel.err = ""
		return m, nil
	case "up", "k":
		if m.contextPanel.scroll > 0 {
			m.contextPanel.scroll--
		}
		return m, nil
	case "down", "j":
		m.contextPanel.scroll++
		return m, nil
	case "pgup":
		m.contextPanel.scroll = maxInt(0, m.contextPanel.scroll-10)
		return m, nil
	case "pgdown":
		m.contextPanel.scroll += 10
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
