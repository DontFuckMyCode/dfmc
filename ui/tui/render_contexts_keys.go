// render_contexts_keys.go — keyboard surface for the "Active Contexts"
// overlay (Shift+F6). Up/down move the section cursor (MAIN / PARKED /
// SUBAGENT / DRIVE); right/enter opens a context-aware action menu so
// the user can resume the parked agent or cancel an active drive run
// without leaving the panel. j/k/pgup/pgdn/g/G still page the body for
// long renders. The previous handler was scroll-only — the panel
// surfaced "/continue resumes from here" copy with no way to actually
// run it from the panel, exactly the static-wall pattern called out in
// the panels-must-be-interactive feedback.

package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// openContextsActionMenu builds the action list for whichever section
// the cursor is on. Each section knows which engine state it's
// reflecting (parked agent presence, drive runs), so the actions are
// pruned to the ones that would actually succeed — no dead "Stop drive
// run" entry when nothing is running.
func (m Model) openContextsActionMenu() Model {
	var actions []panelAction
	title := "Context actions"
	switch m.contexts.selectedSection {
	case contextsSectionMain:
		title = "Main agent actions"
		actions = []panelAction{
			{Label: "Switch to Chat tab", Accel: "c",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("Chat"), nil
				}},
			{Label: "Open Activity (F5)", Accel: "a",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("Activity"), nil
				}},
		}
	case contextsSectionParked:
		title = "Parked agent actions"
		if m.eng != nil && m.eng.HasParkedAgent() {
			actions = []panelAction{
				{Label: "Continue parked agent (/continue)", Accel: "c",
					Handler: func(m Model) (Model, tea.Cmd) {
						m, cmd := m.startChatResume("")
						return m.activateDiagnosticTab("Chat"), cmd
					}},
				{Label: "Discard parked agent (free state)", Accel: "d",
					Handler: func(m Model) (Model, tea.Cmd) {
						if m.eng != nil {
							m.eng.ClearParkedAgent()
							m.notice = "Parked agent discarded."
						}
						return m, nil
					}},
			}
		} else {
			actions = []panelAction{
				{Label: "Switch to Chat tab (no parked agent)", Accel: "c",
					Handler: func(m Model) (Model, tea.Cmd) {
						return m.activateDiagnosticTab("Chat"), nil
					}},
			}
		}
	case contextsSectionSubagent:
		title = "Sub-agent actions"
		actions = []panelAction{
			{Label: "Open Workflow (F4)", Accel: "w",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("Workflow"), nil
				}},
			{Label: "Open Activity (F5)", Accel: "a",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("Activity"), nil
				}},
		}
	case contextsSectionDrive:
		title = "Drive actions"
		active := drive.ListActive()
		if len(active) == 1 {
			runID := active[0].RunID
			actions = []panelAction{
				{Label: "Stop active drive run", Accel: "s",
					Handler: func(m Model) (Model, tea.Cmd) {
						if drive.Cancel(runID) {
							m.notice = "Drive [" + shortRunID(runID) + "] stopping — finishes current TODO first."
						} else {
							m.notice = "Drive run is not active anymore."
						}
						return m, nil
					}},
				{Label: "Open Workflow (F4)", Accel: "w",
					Handler: func(m Model) (Model, tea.Cmd) {
						return m.activateDiagnosticTab("Workflow"), nil
					}},
			}
		} else if len(active) > 1 {
			// Multiple active runs — stopping needs a specific ID, so
			// route the user to Workflow where each run has its own row.
			actions = []panelAction{
				{Label: "Open Workflow to pick a run", Accel: "w",
					Handler: func(m Model) (Model, tea.Cmd) {
						return m.activateDiagnosticTab("Workflow"), nil
					}},
			}
		} else {
			actions = []panelAction{
				{Label: "Open Workflow (F4) to browse runs", Accel: "w",
					Handler: func(m Model) (Model, tea.Cmd) {
						return m.activateDiagnosticTab("Workflow"), nil
					}},
			}
		}
	}
	if len(actions) == 0 {
		return m
	}
	return m.openActionMenu("Contexts", title, actions)
}

// handleContextsOverlayKey replaces the scroll-only handler. The action
// menu wins keystrokes while open (so arrows / enter pick actions); when
// it's closed, up/down move the section cursor, j/k/pgup/pgdn scroll the
// body, and right/enter opens the menu. esc/q are still handled by the
// global overlay close.
func (m Model) handleContextsOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	key := msg.String()
	switch key {
	case "up":
		if m.contexts.selectedSection > 0 {
			m.contexts.selectedSection--
		}
		return m, nil
	case "down":
		if m.contexts.selectedSection < contextsSectionCount-1 {
			m.contexts.selectedSection++
		}
		return m, nil
	case "right", "enter", "l":
		return m.openContextsActionMenu(), nil
	}
	// Fall through to the shared scroll grammar (j/k/pgup/pgdn/g/G) so
	// long renders are still pageable. j/k are deliberately routed to
	// scroll rather than section-cursor — section count is small (4)
	// and arrow keys are the discoverable surface; vim users get j/k
	// for body scroll like the other read-only overlays.
	m.contexts.scroll = adjustScrollOnlyOffset(key, m.contexts.scroll)
	return m, nil
}
