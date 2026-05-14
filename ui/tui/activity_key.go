package tui

// Activity panel keyboard handlers. Split from activity.go so the
// j/k/enter/1-6 mode switches live next to each other and away from
// event ingestion (activity.go) and render helpers (activity_render.go).
// handleActivityKey is the per-tab dispatch entry point called from
// update.go; handleActivitySearchKey takes over while the user is
// typing a search query (after pressing `/`).

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// openActivityActionMenu — arrow-driven discovery surface for the
// Activity tab's secondary actions. View filter cycling, follow
// toggle, search, clear, and per-entry actions all live here so the
// user doesn't have to remember the 1-6 number filters or letters.
func (m Model) openActivityActionMenu() Model {
	followLabel := "Pause live tail (currently LIVE)"
	if !m.activity.follow {
		followLabel = "Resume live tail (currently PAUSED)"
	}
	actions := []panelAction{
		{Label: followLabel, Accel: "p", Handler: func(m Model) (Model, tea.Cmd) {
			m.activity.follow = !m.activity.follow
			if m.activity.follow {
				m.activity.scroll = 0
			}
			return m, nil
		}},
		{Label: "Cycle filter view (all → tools → agents → errors → workflow → context)", Accel: "v",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.activity.mode = nextActivityMode(m.activity.mode)
				m.activity.scroll = 0
				m.activity.follow = true
				return m, nil
			}},
		{Label: "Filter: All events", Accel: "1", Handler: func(m Model) (Model, tea.Cmd) {
			m.activity.mode = activityViewAll
			m.activity.scroll = 0
			m.activity.follow = true
			return m, nil
		}},
		{Label: "Filter: Tools only", Accel: "2", Handler: func(m Model) (Model, tea.Cmd) {
			m.activity.mode = activityViewTools
			m.activity.scroll = 0
			m.activity.follow = true
			return m, nil
		}},
		{Label: "Filter: Agents / subagents", Accel: "3", Handler: func(m Model) (Model, tea.Cmd) {
			m.activity.mode = activityViewAgents
			m.activity.scroll = 0
			m.activity.follow = true
			return m, nil
		}},
		{Label: "Filter: Errors only", Accel: "4", Handler: func(m Model) (Model, tea.Cmd) {
			m.activity.mode = activityViewErrors
			m.activity.scroll = 0
			m.activity.follow = true
			return m, nil
		}},
		{Label: "Filter: Workflow / drive", Accel: "5", Handler: func(m Model) (Model, tea.Cmd) {
			m.activity.mode = activityViewWorkflow
			m.activity.scroll = 0
			m.activity.follow = true
			return m, nil
		}},
		{Label: "Filter: Context lifecycle", Accel: "6", Handler: func(m Model) (Model, tea.Cmd) {
			m.activity.mode = activityViewContext
			m.activity.scroll = 0
			m.activity.follow = true
			return m, nil
		}},
		{Label: "Search…", Accel: "/", Handler: func(m Model) (Model, tea.Cmd) {
			m.activity.searchActive = true
			return m, nil
		}},
		{Label: "Clear search query (or all entries when empty)", Accel: "c",
			Handler: func(m Model) (Model, tea.Cmd) {
				if strings.TrimSpace(m.activity.query) != "" {
					m.activity.query = ""
					m.activity.scroll = 0
					m.activity.follow = true
					return m, nil
				}
				m.activity.entries = nil
				m.activity.scroll = 0
				m.activity.follow = true
				return m, nil
			}},
		{Label: "Open selected entry detail", Accel: "enter",
			Handler: func(m Model) (Model, tea.Cmd) {
				nm, cmd := m.activityOpenSelection(false)
				if mm, ok := nm.(Model); ok {
					return mm, cmd
				}
				return m, cmd
			}},
		{Label: "Focus selected entry's file in Files tab", Accel: "f",
			Handler: func(m Model) (Model, tea.Cmd) {
				nm, cmd := m.activityFocusSelectionFile()
				if mm, ok := nm.(Model); ok {
					return mm, cmd
				}
				return m, cmd
			}},
		{Label: "Copy selected entry to chat composer", Accel: "y",
			Handler: func(m Model) (Model, tea.Cmd) {
				nm, cmd := m.activityCopySelection()
				if mm, ok := nm.(Model); ok {
					return mm, cmd
				}
				return m, cmd
			}},
	}
	return m.openActionMenu("Activity", "Activity actions", actions)
}

func (m Model) handleActivityKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.activity.searchActive {
		return m.handleActivitySearchKey(msg)
	}
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	// Right opens the action menu — view filter, follow toggle,
	// search, clear, copy entry — without making the user memorise
	// 1-6 / p / / / c / y.
	if s := msg.String(); s == "right" || s == "l" {
		return m.openActivityActionMenu(), nil
	}
	total := len(m.filteredActivityEntries())
	switch msg.String() {
	case "enter", "o":
		return m.activityOpenSelection(false)
	case "r":
		return m.activityOpenSelection(true)
	case "f":
		return m.activityFocusSelectionFile()
	case "y":
		return m.activityCopySelection()
	case "j", "down":
		m.activity.scroll = scrollIndexUp(m.activity.scroll, 1)
		m.activity.follow = m.activity.scroll == 0
	case "k", "up":
		next := scrollIndexDown(m.activity.scroll, total, 1)
		if next != m.activity.scroll {
			m.activity.scroll = next
			m.activity.follow = false
		}
	case "pgdown":
		m.activity.scroll = scrollIndexUp(m.activity.scroll, 10)
		m.activity.follow = m.activity.scroll == 0
	case "pgup":
		m.activity.scroll = scrollIndexDown(m.activity.scroll, total, 10)
		m.activity.follow = false
	case "g", "home":
		m.activity.scroll = lastScrollIndex(total)
		m.activity.follow = false
	case "G", "end":
		m.activity.scroll = 0
		m.activity.follow = true
	case "1":
		m = m.setActivityMode(activityViewAll)
	case "2":
		m = m.setActivityMode(activityViewTools)
	case "3":
		m = m.setActivityMode(activityViewAgents)
	case "4":
		m = m.setActivityMode(activityViewErrors)
	case "5":
		m = m.setActivityMode(activityViewWorkflow)
	case "6":
		m = m.setActivityMode(activityViewContext)
	case "v":
		m = m.setActivityMode(nextActivityMode(m.activity.mode))
	case "/":
		m.activity.searchActive = true
	case "c":
		if strings.TrimSpace(m.activity.query) != "" {
			m.activity.query = ""
			m.activity.scroll = 0
			m.activity.follow = true
			break
		}
		m.activity.entries = nil
		m.activity.scroll = 0
		m.activity.follow = true
	case "p":
		m.activity.follow = !m.activity.follow
		if m.activity.follow {
			m.activity.scroll = 0
		}
	}
	m.activity.scroll = clampActivityOffset(m.activity.scroll, len(m.filteredActivityEntries()))
	return m, nil
}

func (m Model) setActivityMode(mode activityViewMode) Model {
	m.activity.mode = mode
	m.activity.scroll = 0
	m.activity.follow = true
	return m
}

func (m Model) handleActivitySearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.activity.searchActive = false
		m.activity.scroll = 0
		m.activity.follow = true
		return m, nil
	case tea.KeyEsc:
		m.activity.searchActive = false
		return m, nil
	default:
		if query, ok := applyInlineSearchTextKey(m.activity.query, msg); ok {
			m.activity.query = query
		}
	}
	return m, nil
}
