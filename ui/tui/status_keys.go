// status_keys.go — keyboard surface for the rebuilt F2 Status panel.
//
// Arrow keys (and h/j/k/l) move the card selection. Enter activates
// the selected card by jumping to the related detail tab. r refreshes
// the status snapshot. The mapping mirrors what the panel's footer
// hints advertise so a user never has to read code to learn the keys.

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleStatusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		return m, loadStatusCmd(m.eng)
	case "left", "h":
		return m.shiftStatusCard(-1), nil
	case "right", "l":
		return m.shiftStatusCard(1), nil
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
		count := m.diagnosticPanelsState.statusPanel.cardCount
		if count > 0 {
			m.diagnosticPanelsState.statusPanel.selectedCard = count - 1
		}
		return m, nil
	case "enter":
		return m.activateSelectedStatusCard(), nil
	}
	return m, nil
}

func (m Model) shiftStatusCard(delta int) Model {
	count := m.diagnosticPanelsState.statusPanel.cardCount
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
