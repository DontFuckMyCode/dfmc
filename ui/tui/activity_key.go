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

func (m Model) handleActivityKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.activity.searchActive {
		return m.handleActivitySearchKey(msg)
	}
	total := len(m.filteredActivityEntries())
	step := 1
	pageStep := 10
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
		if m.activity.scroll >= step {
			m.activity.scroll -= step
		} else {
			m.activity.scroll = 0
		}
		m.activity.follow = m.activity.scroll == 0
	case "k", "up":
		if m.activity.scroll+step < total {
			m.activity.scroll += step
			m.activity.follow = false
		}
	case "pgdown":
		if m.activity.scroll >= pageStep {
			m.activity.scroll -= pageStep
		} else {
			m.activity.scroll = 0
		}
		m.activity.follow = m.activity.scroll == 0
	case "pgup":
		if m.activity.scroll+pageStep <= total {
			m.activity.scroll += pageStep
		} else if total > 0 {
			m.activity.scroll = total - 1
		}
		m.activity.follow = false
	case "g", "home":
		if total > 0 {
			m.activity.scroll = total - 1
		}
		m.activity.follow = false
	case "G", "end":
		m.activity.scroll = 0
		m.activity.follow = true
	case "1":
		m.activity.mode = activityViewAll
		m.activity.scroll = 0
		m.activity.follow = true
	case "2":
		m.activity.mode = activityViewTools
		m.activity.scroll = 0
		m.activity.follow = true
	case "3":
		m.activity.mode = activityViewAgents
		m.activity.scroll = 0
		m.activity.follow = true
	case "4":
		m.activity.mode = activityViewErrors
		m.activity.scroll = 0
		m.activity.follow = true
	case "5":
		m.activity.mode = activityViewWorkflow
		m.activity.scroll = 0
		m.activity.follow = true
	case "6":
		m.activity.mode = activityViewContext
		m.activity.scroll = 0
		m.activity.follow = true
	case "v":
		m.activity.mode = nextActivityMode(m.activity.mode)
		m.activity.scroll = 0
		m.activity.follow = true
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
	case tea.KeyBackspace:
		if r := []rune(m.activity.query); len(r) > 0 {
			m.activity.query = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.activity.query += msg.String()
		return m, nil
	}
	return m, nil
}
