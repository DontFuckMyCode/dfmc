// status_keys.go — keyboard surface for the rebuilt F9 Status panel.
//
// Arrow keys (and h/j/k/l) move the card selection. Enter activates
// the selected card by jumping to the related detail tab. r refreshes
// the status snapshot. → opens the action menu (Phase D — every panel
// gets a discoverable surface; accelerators stay for power users).

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// openStatusActionMenu — arrow-driven discovery surface for Status
// card-grid actions. Matches the pattern in activity_key.go etc.
func (m Model) openStatusActionMenu() Model {
	actions := []panelAction{
		{Label: "Open the selected card's detail tab",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.activateSelectedStatusCard(), nil
			}},
		{Label: "Refresh status snapshot", Accel: "r",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m, loadStatusCmd(m.eng)
			}},
		{Label: "Jump to Files (Project card)", Accel: "1",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.activateDiagnosticTab("Files"), nil
			}},
		{Label: "Jump to Providers (Provider card)", Accel: "2",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.activateDiagnosticTab("Providers"), nil
			}},
		{Label: "Jump to CodeMap (AST card)", Accel: "3",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.activateDiagnosticTab("CodeMap"), nil
			}},
		{Label: "Jump to Memory", Accel: "4",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.activateDiagnosticTab("Memory"), nil
			}},
		{Label: "Jump to Orchestrate (subagents / drive overview)", Accel: "5",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.activateDiagnosticTab("Orchestrate"), nil
			}},
		{Label: "First card", Accel: "g",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.diagnosticPanelsState.statusPanel.selectedCard = 0
				return m, nil
			}},
		{Label: "Last card", Accel: "G",
			Handler: func(m Model) (Model, tea.Cmd) {
				cards, _ := m.statusCards()
				if len(cards) > 0 {
					m.diagnosticPanelsState.statusPanel.selectedCard = len(cards) - 1
					m.diagnosticPanelsState.statusPanel.cardCount = len(cards)
				}
				return m, nil
			}},
	}
	return m.openActionMenu("Status", "Status actions", actions)
}

func (m Model) handleStatusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	switch msg.String() {
	case "r":
		return m, loadStatusCmd(m.eng)
	case "left", "h":
		return m.shiftStatusCard(-1), nil
	case "l":
		// vim-style move right inside the 2D card grid. The action
		// menu deliberately uses bare `right` so `l` keeps its move
		// meaning (h/j/k/l form the Status panel's directional set).
		return m.shiftStatusCard(1), nil
	case "right":
		// Right opens the action menu — arrow-driven discovery of
		// refresh / jump-to-detail-tab / enter without making the user
		// remember the per-card index mapping.
		return m.openStatusActionMenu(), nil
	case "up", "k":
		// 2-column grid: up = -2 (one row up). Falls back to -1
		// when there's only one column on narrow terminals.
		return m.shiftStatusCard(-2), nil
	case "down", "j":
		return m.shiftStatusCard(2), nil
	case "home", "g":
		m.diagnosticPanelsState.statusPanel.selectedCard = 0
		return m, nil
	case "end", "G":
		cards, _ := m.statusCards()
		if len(cards) > 0 {
			m.diagnosticPanelsState.statusPanel.selectedCard = len(cards) - 1
			m.diagnosticPanelsState.statusPanel.cardCount = len(cards)
		}
		return m, nil
	case "enter":
		return m.activateSelectedStatusCard(), nil
	}
	return m, nil
}

func (m Model) shiftStatusCard(delta int) Model {
	// Compute card count directly from model state rather than
	// relying on cardCount which is only set during View (on a
	// value copy that never reaches the real model).
	cards, _ := m.statusCards()
	count := len(cards)
	if count <= 0 {
		return m
	}
	idx := m.diagnosticPanelsState.statusPanel.selectedCard + delta
	if idx < 0 {
		idx = 0
	}
	if idx >= count {
		idx = count - 1
	}
	m.diagnosticPanelsState.statusPanel.selectedCard = idx
	// Sync cardCount so action menu "last card" (G) also works.
	m.diagnosticPanelsState.statusPanel.cardCount = count
	return m
}

// activateSelectedStatusCard jumps to the detail tab a Status card
// represents. The mapping is positional — index 0 = Project (→ Files),
// index 1 = Provider (→ Providers), index 2 = AST (→ CodeMap), and so
// on — matching the order statusCards() builds.
func (m Model) activateSelectedStatusCard() Model {
	idx := m.diagnosticPanelsState.statusPanel.selectedCard
	count := m.diagnosticPanelsState.statusPanel.cardCount
	if idx < 0 || count <= 0 {
		return m
	}
	switch idx {
	case 0: // Project
		return m.activateDiagnosticTab("Files")
	case 1: // Provider
		return m.activateDiagnosticTab("Providers")
	case 2, 3: // AST, CodeMap
		return m.activateDiagnosticTab("CodeMap")
	default:
		// Optional cards (Memory, Context In, Subagents) — pick the
		// most relevant based on the title (we don't store mappings
		// per-card to avoid drifting from statusCards). Fallback:
		// Orchestrate as the catch-all hierarchy view.
		return m.activateDiagnosticTab("Orchestrate")
	}
}
