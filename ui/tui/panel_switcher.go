package tui

// panel_switcher.go — Ctrl+B "panel switcher" overlay. Listed every
// panel with its F-key (and any alternate) plus a fuzzy-filter input
// so users whose terminal eats specific F-keys (F11 → fullscreen,
// F1 → terminal help, F4 → close-tab) can still reach every panel by
// typing.
//
// The switcher is intentionally minimal — list, filter, enter, esc —
// and renders as a centered modal overlay over whatever the active
// tab is. Selection routes through activateDiagnosticTab(label) which
// already handles both first-class tabs and demoted overlays, so adding
// new panels later only needs the entry list update here.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// panelSwitcherState holds the live picker state. Idle when active=false.
type panelSwitcherState struct {
	active bool
	query  string
	index  int
}

// panelSwitcherEntry maps a user-facing panel label to the
// activateDiagnosticTab argument plus a short keyhint string.
type panelSwitcherEntry struct {
	Label    string
	KeyHint  string
	Activate string // value passed to activateDiagnosticTab
	Hint     string
}

// panelSwitcherEntries lists every reachable panel in canonical order.
// activateDiagnosticTab handles both first-class tabs (Chat..Providers,
// activeTab=N) and demoted overlays (Status, Tools, Contexts, ...).
// Adding a new panel here AND in demotedPanelKinds is sufficient to make
// it switchable; no other plumbing needed.
func panelSwitcherEntries() []panelSwitcherEntry {
	return []panelSwitcherEntry{
		{Label: "Chat", KeyHint: "F1 · Alt+1", Activate: "Chat", Hint: "main composer + transcript"},
		{Label: "Files", KeyHint: "F2 · Alt+2", Activate: "Files", Hint: "project file picker + preview"},
		{Label: "Patch", KeyHint: "F3 · Alt+3", Activate: "Patch", Hint: "worktree diff · staged hunks"},
		{Label: "Workflow", KeyHint: "F4 · Alt+4", Activate: "Workflow", Hint: "drive cockpit + run list"},
		{Label: "Activity", KeyHint: "F5 · Alt+5", Activate: "Activity", Hint: "event firehose"},
		{Label: "Memory", KeyHint: "F6 · Alt+6", Activate: "Memory", Hint: "working/episodic/semantic"},
		{Label: "Conversations", KeyHint: "F7 · Alt+7", Activate: "Conversations", Hint: "saved conversations + branches"},
		{Label: "Providers", KeyHint: "F8 · Alt+8 · Ctrl+O", Activate: "Providers", Hint: "provider catalog + keys"},
		{Label: "Status", KeyHint: "F9 · Ctrl+I", Activate: "Status", Hint: "engine + provider snapshot"},
		{Label: "CodeMap", KeyHint: "F10", Activate: "CodeMap", Hint: "symbol + dep graph"},
		{Label: "Tools", KeyHint: "F11 · Alt+I", Activate: "Tools", Hint: "tool registry + params"},
		{Label: "Security", KeyHint: "F12", Activate: "Security", Hint: "scanner · secrets · vulns"},
		{Label: "Prompts", KeyHint: "Shift+F1 · Alt+T", Activate: "Prompts", Hint: "prompt overlay catalog"},
		{Label: "Plans", KeyHint: "Shift+F2 · Ctrl+Y", Activate: "Plans", Hint: "task split editor"},
		{Label: "Context", KeyHint: "Shift+F3 · Ctrl+W", Activate: "Context", Hint: "context build preview"},
		{Label: "Orchestrate", KeyHint: "Shift+F4", Activate: "Orchestrate", Hint: "agents/subagents/todos/drive"},
		{Label: "Shortcuts", KeyHint: "Shift+F5 · Alt+H", Activate: "Shortcuts", Hint: "this cheat sheet"},
		{Label: "Contexts", KeyHint: "Shift+F6", Activate: "Contexts", Hint: "live agents · main · parked · subagents"},
		{Label: "ProviderLog", KeyHint: "Shift+F7 · Ctrl+L", Activate: "ProviderLog", Hint: "every provider call (model · in/out tokens · preview)"},
	}
}

// filteredPanelSwitcherEntries returns entries whose label or hint
// contains the lowercased query as a substring. Empty query returns the
// full list. Order follows the canonical order from panelSwitcherEntries.
func filteredPanelSwitcherEntries(query string) []panelSwitcherEntry {
	q := strings.ToLower(strings.TrimSpace(query))
	all := panelSwitcherEntries()
	if q == "" {
		return all
	}
	out := make([]panelSwitcherEntry, 0, len(all))
	for _, e := range all {
		hay := strings.ToLower(e.Label + " " + e.Hint + " " + e.KeyHint)
		if strings.Contains(hay, q) {
			out = append(out, e)
		}
	}
	return out
}

func (m Model) openPanelSwitcher() Model {
	m.panelSwitcher.active = true
	m.panelSwitcher.query = ""
	m.panelSwitcher.index = 0
	m.notice = "Panel switcher — type to filter · ↑↓ navigate · enter open · esc close"
	return m
}

func (m Model) closePanelSwitcher() Model {
	m.panelSwitcher.active = false
	m.panelSwitcher.query = ""
	m.panelSwitcher.index = 0
	return m
}

func (m Model) handlePanelSwitcherKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if !m.panelSwitcher.active {
		return m, nil, false
	}
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlB:
		// Ctrl+B is the toggle key; pressing it again while the
		// switcher is open should close it. Esc is a generic dismiss.
		m = m.closePanelSwitcher()
		m.notice = "Panel switcher closed."
		return m, nil, true
	case tea.KeyUp:
		items := filteredPanelSwitcherEntries(m.panelSwitcher.query)
		if len(items) == 0 {
			return m, nil, true
		}
		idx := clampIndex(m.panelSwitcher.index, len(items))
		if idx > 0 {
			idx--
		}
		m.panelSwitcher.index = idx
		return m, nil, true
	case tea.KeyDown:
		items := filteredPanelSwitcherEntries(m.panelSwitcher.query)
		if len(items) == 0 {
			return m, nil, true
		}
		idx := clampIndex(m.panelSwitcher.index, len(items))
		if idx < len(items)-1 {
			idx++
		}
		m.panelSwitcher.index = idx
		return m, nil, true
	case tea.KeyTab, tea.KeyEnter:
		items := filteredPanelSwitcherEntries(m.panelSwitcher.query)
		if len(items) == 0 {
			m.notice = "No panels match — type a different filter or esc."
			return m, nil, true
		}
		idx := clampIndex(m.panelSwitcher.index, len(items))
		target := items[idx].Activate
		m = m.closePanelSwitcher()
		m = m.activateDiagnosticTab(target)
		m.notice = "Switched to " + target + "."
		return m, nil, true
	case tea.KeyBackspace, tea.KeyCtrlH:
		if len(m.panelSwitcher.query) > 0 {
			runes := []rune(m.panelSwitcher.query)
			m.panelSwitcher.query = string(runes[:len(runes)-1])
			m.panelSwitcher.index = 0
		}
		return m, nil, true
	case tea.KeySpace:
		m.panelSwitcher.query += " "
		m.panelSwitcher.index = 0
		return m, nil, true
	case tea.KeyRunes:
		m.panelSwitcher.query += string(msg.Runes)
		m.panelSwitcher.index = 0
		return m, nil, true
	}
	return m, nil, true
}

// renderPanelSwitcher renders the picker as a centered modal. Caller
// (render_layout.go) overlays it on top of whatever the active body is.
func (m Model) renderPanelSwitcher(width int) string {
	if width < 50 {
		width = 50
	}
	if width > 90 {
		width = 90
	}
	pal := paletteForTab("Chat", false)
	frame := lipgloss.NewStyle().
		Padding(1, 2).
		Background(colorPanelBg).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Border)

	items := filteredPanelSwitcherEntries(m.panelSwitcher.query)
	idx := clampIndex(m.panelSwitcher.index, len(items))

	title := accentStyle.Bold(true).Render("◆ Switch Panel") +
		subtleStyle.Render("  -  ") +
		boldStyle.Render(m.panelSwitcher.query+"_")

	count := subtleStyle.Render("  no panels match")
	if len(items) > 0 {
		count = subtleStyle.Render("  " + itoaLocal(len(items)) + " / " + itoaLocal(len(panelSwitcherEntries())) + " panels")
	}

	body := []string{title, count, ""}
	if len(items) == 0 {
		body = append(body,
			subtleStyle.Render("  Type a panel name fragment, e.g. 'cont' for Contexts/Conversations/Context."),
			subtleStyle.Render("  Or hit esc to close."),
		)
	} else {
		for i, e := range items {
			line := "  " + e.Label
			padding := strings.Repeat(" ", maxIntLocal(0, 16-len(e.Label)))
			line += padding + subtleStyle.Render(e.KeyHint)
			line += "  " + subtleStyle.Render(truncateForLine(e.Hint, 36))
			if i == idx {
				body = append(body, accentStyle.Bold(true).Render("▶ "+strings.TrimPrefix(line, "  ")))
			} else {
				body = append(body, line)
			}
		}
	}
	body = append(body, "", subtleStyle.Render("up/down navigate · tab/enter open · esc close · ctrl+b toggle"))
	return frame.Width(width).Render(strings.Join(body, "\n"))
}

// itoaLocal / maxIntLocal — tiny private helpers so this file doesn't
// pull in strconv or fight whatever max() helpers other files define.
func itoaLocal(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func maxIntLocal(a, b int) int {
	if a > b {
		return a
	}
	return b
}
