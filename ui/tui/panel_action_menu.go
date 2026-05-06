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
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// panelAction is one row in the menu — a label, an optional keyboard
// accelerator (e.g. "p" for pin), and a handler that returns the new
// model + an optional command.
type panelAction struct {
	Label    string
	Accel    string // optional single-letter accelerator hint
	Handler  func(Model) (Model, tea.Cmd)
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

// renderActionMenu draws the menu as a centered overlay block. Caller
// composites it on top of the panel via the same overlay path the help
// overlay uses.
func (m Model) renderActionMenu(width int) string {
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
	header := titleStyle.Bold(true).Render("◇ " + title)
	hint := subtleStyle.Render("↑↓ pick · enter run · esc close")

	rows := []string{header, hint, renderDivider(innerWidth - 2), ""}
	if len(m.actionMenu.actions) == 0 {
		rows = append(rows, subtleStyle.Render("(no actions)"))
	} else {
		for i, a := range m.actionMenu.actions {
			cursor := "  "
			label := a.Label
			if i == m.actionMenu.selected {
				cursor = accentStyle.Bold(true).Render("▶ ")
				label = accentStyle.Bold(true).Render(label)
			}
			accel := ""
			if a.Accel != "" {
				accel = "  " + subtleStyle.Render("["+a.Accel+"]")
			}
			rows = append(rows, cursor+label+accel)
		}
	}
	body := strings.Join(rows, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPanelBorder).
		Padding(0, 1).
		Width(innerWidth).
		Render(body)
}
