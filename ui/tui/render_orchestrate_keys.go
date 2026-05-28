// render_orchestrate_keys.go — keyboard surface for the Orchestrate
// overlay (Alt+R / Shift+F4). Mirrors the render_contexts_keys.go
// pattern: up/down move a section cursor across the seven top-level
// sections (MAIN AGENT, SUBAGENTS, TODOS, TASK STORE, DRIVE RUN,
// TOKENS, RECENT ACTIVITY); right/enter opens a context-aware action
// menu so the user can jump to the appropriate detail panel or stop
// the active drive run without leaving the overlay. j/k/pgup/pgdn/g/G
// still page the body for long renders. The previous handler was
// scroll-only — Orchestrate is the second-largest static-wall panel
// after Contexts, surfacing "F5 for cockpit" and "/drive <task> in
// chat" copy with no way to actually run any of it from the panel.

package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// openOrchestrateActionMenu builds the action list for whichever
// section the cursor is on. Actions are pruned to operations that
// would actually succeed in the current state — no dead "Stop drive
// run" entry when nothing is running.
func (m Model) openOrchestrateActionMenu() Model {
	var actions []panelAction
	title := "Orchestrate actions"
	switch m.orchestrate.selectedSection {
	case orchestrateSectionMain:
		title = "Main agent actions"
		actions = []panelAction{
			{Label: "Switch to Chat tab", Accel: "c",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("Chat"), nil
				}},
		}
	case orchestrateSectionSubagents:
		title = "Sub-agent actions"
		actions = []panelAction{
			{Label: "Open Activity (F5)", Accel: "a",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("Activity"), nil
				}},
			{Label: "Open Workflow (F4)", Accel: "w",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("Workflow"), nil
				}},
		}
	case orchestrateSectionTodos, orchestrateSectionTaskStore:
		title = "Work-tracking actions"
		actions = []panelAction{
			{Label: "Open Workflow (F4)", Accel: "w",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("Workflow"), nil
				}},
			{Label: "Switch to Chat tab", Accel: "c",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("Chat"), nil
				}},
		}
	case orchestrateSectionDrive:
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
		} else {
			actions = []panelAction{
				{Label: "Open Workflow (F4)", Accel: "w",
					Handler: func(m Model) (Model, tea.Cmd) {
						return m.activateDiagnosticTab("Workflow"), nil
					}},
			}
		}
	case orchestrateSectionTokens:
		title = "Token / budget actions"
		actions = []panelAction{
			{Label: "Open Context (F7)", Accel: "x",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("Context"), nil
				}},
			{Label: "Open Status (F9)", Accel: "i",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("Status"), nil
				}},
		}
	case orchestrateSectionRecent:
		title = "Recent activity actions"
		actions = []panelAction{
			{Label: "Open Activity (F5)", Accel: "a",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("Activity"), nil
				}},
			{Label: "Open ProviderLog (Shift+F7)", Accel: "l",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.activateDiagnosticTab("ProviderLog"), nil
				}},
		}
	}
	if len(actions) == 0 {
		return m
	}
	return m.openActionMenu("Orchestrate", title, actions)
}

// handleOrchestrateKey replaces the scroll-only handler. The action
// menu wins keystrokes while open (so arrows / enter pick actions);
// when it's closed, up/down move the section cursor, j/k/pgup/pgdn
// scroll the body, and right/enter opens the menu. esc/q stay on the
// global overlay close path.
func (m Model) handleOrchestrateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	key := msg.String()
	switch key {
	case "up":
		if m.orchestrate.selectedSection > 0 {
			m.orchestrate.selectedSection--
		}
		return m, nil
	case "down":
		if m.orchestrate.selectedSection < orchestrateSectionCount-1 {
			m.orchestrate.selectedSection++
		}
		return m, nil
	case "right", "enter", "l":
		return m.openOrchestrateActionMenu(), nil
	}
	// Fall through to shared scroll grammar (j/k/pgup/pgdn/g/G).
	m.orchestrate.scroll = adjustScrollOnlyOffset(key, m.orchestrate.scroll)
	return m, nil
}
