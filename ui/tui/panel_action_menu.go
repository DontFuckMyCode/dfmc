// panel_action_menu.go — generic action picker used by every list-style
// panel so the user can do everything with just arrow keys + enter + esc.
//
// Why: the per-panel single-letter shortcut soup (p/v/r/i/e/x/t/c/etc.)
// is non-discoverable and forces the user to memorise different keys per
// tab. Pressing Right (or the legacy Enter) on a selected row opens the
// menu; arrows pick; enter runs; esc closes. Old single-letter
// accelerators stay registered for power users but the menu is the
// canonical, discoverable path.

package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// panelAction is one row in the menu — a label, an optional keyboard
// accelerator (e.g. "p" for pin), and a handler that returns the new
// model + an optional command.
type panelAction struct {
	Label   string
	Accel   string // optional single-letter accelerator hint
	Handler func(Model) (Model, tea.Cmd)
}

// panelActionMenu owns the open/closed state plus the action list and
// the cursor inside it. A panel populates `actions` when it opens the
// menu (via `openActionMenu`), so the menu is always context-aware
// (e.g. "Pin" vs "Unpin" depending on whether the row is pinned).
type panelActionMenu struct {
	open     bool
	owner    string // panel name that opened the menu, e.g. "Files"
	title    string
	actions  []panelAction
	selected int
}

// openActionMenu populates the menu and shows it. owner is the panel
// name (for routing the close — every panel shares this state).
func (m Model) openActionMenu(owner, title string, actions []panelAction) Model {
	m.actionMenu = panelActionMenu{
		open:     true,
		owner:    owner,
		title:    title,
		actions:  actions,
		selected: 0,
	}
	return m
}

// closeActionMenu wipes the menu state without firing anything.
func (m Model) closeActionMenu() Model {
	m.actionMenu = panelActionMenu{}
	return m
}

// handleActionMenuKey routes arrow / enter / esc through the menu.
// Returns (newModel, cmd, handled) — when handled=true the caller
// should NOT fall through to its panel-specific handler.
func (m Model) handleActionMenuKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if !m.actionMenu.open {
		return m, nil, false
	}
	switch msg.String() {
	case "up", "k":
		if m.actionMenu.selected > 0 {
			m.actionMenu.selected--
		}
		return m, nil, true
	case "down", "j":
		if m.actionMenu.selected < len(m.actionMenu.actions)-1 {
			m.actionMenu.selected++
		}
		return m, nil, true
	case "home", "g":
		m.actionMenu.selected = 0
		return m, nil, true
	case "end", "G":
		if n := len(m.actionMenu.actions); n > 0 {
			m.actionMenu.selected = n - 1
		}
		return m, nil, true
	case "enter":
		if len(m.actionMenu.actions) == 0 {
			m = m.closeActionMenu()
			return m, nil, true
		}
		idx := m.actionMenu.selected
		if idx < 0 || idx >= len(m.actionMenu.actions) {
			idx = 0
		}
		action := m.actionMenu.actions[idx]
		m = m.closeActionMenu()
		if action.Handler != nil {
			nm, cmd := action.Handler(m)
			return nm, cmd, true
		}
		return m, nil, true
	case "esc", "left", "h":
		m = m.closeActionMenu()
		return m, nil, true
	}
	// Allow single-letter accelerator inside the menu.
	for _, a := range m.actionMenu.actions {
		if a.Accel != "" && msg.String() == a.Accel {
			m = m.closeActionMenu()
			if a.Handler != nil {
				nm, cmd := a.Handler(m)
				return nm, cmd, true
			}
			return m, nil, true
		}
	}
	return m, nil, true // swallow stray keys while menu is open
}

// renderActionMenu draws the menu as a bordered block at its natural height.
// Kept for direct callers/tests; the on-screen path is overlayActionMenu,
// which bounds the height so the menu can never run off the frame.
func (m Model) renderActionMenu(width int) string {
	return m.renderActionMenuBounded(width, 1<<20)
}

// renderActionMenuBounded draws the menu but never taller than maxHeight: when
// the action list would overflow, it shows a scrolling window around the
// selected row with ↑/↓ "more" markers. This is what keeps the menu fully on
// screen on short terminals — the old unbounded block overran the panel frame
// and the height clip left only the title or first row visible.
func (m Model) renderActionMenuBounded(width, maxHeight int) string {
	if !m.actionMenu.open {
		return ""
	}
	innerWidth := max(width/2, 36)
	if innerWidth > 60 {
		innerWidth = 60
	}
	title := strings.ToUpper(strings.TrimSpace(m.actionMenu.title))
	if title == "" {
		title = "ACTIONS"
	}
	// Lines are truncated to the CONTENT width (innerWidth minus the 2 cells
	// of horizontal padding), so each occupies exactly one row. Truncating to
	// innerWidth instead let a near-full-width line (the hint) spill into the
	// padding and wrap, silently inflating the menu past maxHeight and leaving
	// a stray "…" row.
	lineW := max(innerWidth-2, 1)
	header := truncateSingleLine(titleStyle.Bold(true).Render("◇ "+title), lineW)
	hint := truncateSingleLine(subtleStyle.Render("↑↓ pick · enter run · [letter] direct · esc close"), lineW)

	// contentBudget is the row count available inside the rounded border.
	contentBudget := maxHeight - 2
	if contentBudget < 1 {
		contentBudget = 1
	}

	rows := []string{header}
	// The hint + divider + blank chrome is only worth its 3 rows when the
	// menu is roomy; on a cramped frame we drop it so the actual actions get
	// the space (header + at least a couple of items).
	if contentBudget >= len(m.actionMenu.actions)+4 || contentBudget >= 8 {
		rows = append(rows, hint, renderDivider(lineW), "")
	}
	itemBudget := contentBudget - len(rows)
	if itemBudget < 1 {
		itemBudget = 1
	}
	rows = append(rows, m.actionMenuItemRows(itemBudget, lineW)...)
	if len(rows) > contentBudget {
		rows = rows[:contentBudget]
	}

	body := strings.Join(rows, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPanelBorder).
		Padding(0, 1).
		Width(innerWidth).
		Render(body)
}

// actionMenuItemRows renders the action list into at most `budget` rows. When
// the list is taller than the budget it shows a scrolling window centred on the
// selection with ↑/↓ "N more" markers — but the markers never overwrite the
// selected row, so the cursor is ALWAYS visible no matter how short the frame.
func (m Model) actionMenuItemRows(budget, innerWidth int) []string {
	actions := m.actionMenu.actions
	n := len(actions)
	if n == 0 {
		return []string{subtleStyle.Render("(no actions)")}
	}
	render := func(i int) string {
		cursor := "  "
		label := actions[i].Label
		if i == m.actionMenu.selected {
			cursor = accentStyle.Bold(true).Render("▶ ")
			label = accentStyle.Bold(true).Render(label)
		}
		accel := ""
		if actions[i].Accel != "" {
			accel = "  " + subtleStyle.Render("["+actions[i].Accel+"]")
		}
		return truncateSingleLine(cursor+label+accel, innerWidth)
	}
	if n <= budget {
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, render(i))
		}
		return out
	}
	start, end := scrollWindow(m.actionMenu.selected, n, budget)
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, render(i))
	}
	// Overwrite the boundary rows with scroll markers, but only when that row
	// is not the selected one (so the cursor never gets hidden behind a marker
	// on a tiny one- or two-row window).
	if start > 0 && start != m.actionMenu.selected && len(out) > 0 {
		out[0] = subtleStyle.Render(fmt.Sprintf("  ↑ %d more", start))
	}
	if end < n && end-1 != m.actionMenu.selected && len(out) > 0 {
		out[len(out)-1] = subtleStyle.Render(fmt.Sprintf("  ↓ %d more", n-end))
	}
	return out
}

// overlayActionMenu anchors the open action menu to the bottom of a panel
// body, guaranteeing it stays fully on screen within `height` rows. The menu
// is height-bounded; the body above it is clipped (with an "↑ more above"
// marker) to make room. Returns body unchanged when no menu is open. This is
// the single composite point — panels no longer append the menu themselves,
// which previously pushed it past the frame's bottom where the clip ate it.
func (m Model) overlayActionMenu(body string, width, height int) string {
	if !m.actionMenu.open || height <= 0 {
		return body
	}
	menu := m.renderActionMenuBounded(width, height-1)
	if menu == "" {
		return body
	}
	menuLines := strings.Split(menu, "\n")
	if len(menuLines) > height {
		menuLines = menuLines[:height]
	}
	avail := height - len(menuLines) - 1 // -1 for the blank separator row
	if avail < 0 {
		avail = 0
	}
	bodyLines := strings.Split(body, "\n")
	if len(bodyLines) > avail {
		if avail >= 1 {
			clipped := append([]string{}, bodyLines[:avail-1]...)
			clipped = append(clipped, subtleStyle.Render("  ↑ more above"))
			bodyLines = clipped
		} else {
			bodyLines = nil
		}
	}
	for len(bodyLines) < avail {
		bodyLines = append(bodyLines, "")
	}
	rows := make([]string, 0, height)
	rows = append(rows, bodyLines...)
	rows = append(rows, "") // separator
	rows = append(rows, menuLines...)
	if len(rows) > height {
		rows = rows[len(rows)-height:]
	}
	return strings.Join(rows, "\n")
}
