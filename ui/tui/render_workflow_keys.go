package tui

// render_workflow_keys.go — keyboard surface for the Workflow tab. Owns
// the right-arrow action menu (openWorkflowActionMenu), the j/k/g/G/
// enter/o/r/esc handler (handleWorkflowKey), and the TODO-tree expand
// toggle (cycleWorkflowTodoExpand). Routing-editor key handling lives
// in render_workflow_routing.go; rendering and detail panes live in
// render_workflow.go.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// openWorkflowActionMenu builds the contextual action list for the
// Workflow / Drive cockpit panel. Actions depend on whether a run is
// currently selected — when one is, "Stop" / "Resume" / "Copy ID" all
// target it. When no run is selected, the menu still exposes the
// global routing editor + refresh.
func (m Model) openWorkflowActionMenu() Model {
	run := m.selectedRunForWorkflow()

	// When the selected run is a placeholder slot (cursor without
	// committed selection), fall back to the cursor-pointed run from
	// the list so the actions still target something useful.
	if run == nil && len(m.workflow.runs) > 0 &&
		m.workflow.selectedIndex >= 0 && m.workflow.selectedIndex < len(m.workflow.runs) {
		run = m.workflow.runs[m.workflow.selectedIndex]
	}

	actions := []panelAction{}
	title := "Workflow actions"
	if run != nil {
		title = "Run actions · " + truncateForLine(run.ID, 32)
		runID := run.ID

		actions = append(actions, panelAction{
			Label: "Open run · view TODO tree",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.workflow.selectedRunID = runID
				m.workflow.scrollY = 0
				return m, nil
			},
		})
		actions = append(actions, panelAction{
			Label: "Stop / cancel this run",
			Handler: func(m Model) (Model, tea.Cmd) {
				if drive.Cancel(runID) {
					m.notice = "Drive [" + shortRunID(runID) + "] stopping — finishes current TODO first."
				} else {
					m.notice = "Run " + shortRunID(runID) + " is not active in this process."
				}
				return m, nil
			},
		})
		actions = append(actions, panelAction{
			Label: "Resume this run",
			Handler: func(m Model) (Model, tea.Cmd) {
				if _, err := runDriveResumeAsync(m.eng, runID); err != nil {
					m.notice = "Resume error: " + err.Error()
					return m, nil
				}
				m.notice = "Drive [" + shortRunID(runID) + "] resumed."
				return m, nil
			},
		})
		actions = append(actions, panelAction{
			Label: "Copy full run ID into chat composer",
			Handler: func(m Model) (Model, tea.Cmd) {
				current := strings.TrimRight(m.chat.input, " ")
				if current != "" {
					current += " "
				}
				m.setChatInput(current + runID)
				m.activeTab = 0
				m.notice = "Copied run ID to chat: " + runID
				return m, nil
			},
		})
		actions = append(actions, panelAction{
			Label: "Deselect run · back to run list",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.workflow.selectedRunID = ""
				m.workflow.scrollY = 0
				m.workflow.selectedTodoID = ""
				return m, nil
			},
		})
	}

	// Always-on actions.
	actions = append(actions,
		panelAction{
			Label: "Routing editor (provider tag → profile)", Accel: "r",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.workflow.showRoutingEditor = true
				m.workflow.routingEditTag = ""
				m.workflow.routingEditProfile = ""
				m.workflow.routingEditIndex = 0
				m.workflow.routingEditMode = false
				if m.workflow.routingDraft == nil {
					m.workflow.routingDraft = m.loadDriveRoutingFromProjectConfig()
					if m.workflow.routingDraft == nil {
						m.workflow.routingDraft = make(map[string]string)
					}
				}
				return m, nil
			},
		},
		panelAction{
			Label: "Refresh runs from store",
			Handler: func(m Model) (Model, tea.Cmd) {
				if m.eng != nil && m.eng.Storage != nil {
					if store, err := drive.NewStore(m.eng.Storage.DB()); err == nil {
						if runs, err := store.List(); err == nil {
							m.workflow.runs = runs
							m.notice = fmt.Sprintf("Drive runs reloaded · %d total", len(runs))
						}
					}
				}
				return m, nil
			},
		},
	)
	return m.openActionMenu("Workflow", title, actions)
}

func (m Model) handleWorkflowKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Routing editor has its own key handler
	if m.workflow.showRoutingEditor {
		return m.handleRoutingEditorKey(msg)
	}
	// Action menu owns arrows/enter/esc when open.
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}

	total := len(m.workflow.runs)
	step := 1

	// Right arrow opens the action menu — covers stop / resume /
	// copy ID / focus run / open routing editor without the user
	// having to memorise letters or copy the ID elsewhere.
	if s := msg.String(); s == "right" || s == "l" {
		return m.openWorkflowActionMenu(), nil
	}

	switch msg.String() {
	case "j", "down":
		if m.workflow.selectedRunID == "" {
			// run selector: move selectedIndex down
			if m.workflow.selectedIndex+step < total {
				m.workflow.selectedIndex += step
			}
		} else {
			// TODO tree: scroll down
			m.workflow.scrollY += step
		}
	case "k", "up":
		if m.workflow.selectedRunID == "" {
			if m.workflow.selectedIndex >= step {
				m.workflow.selectedIndex -= step
			} else {
				m.workflow.selectedIndex = 0
			}
		} else {
			if m.workflow.scrollY >= step {
				m.workflow.scrollY -= step
			} else {
				m.workflow.scrollY = 0
			}
		}
	case "g":
		if m.workflow.selectedRunID == "" {
			m.workflow.selectedIndex = 0
		} else {
			m.workflow.scrollY = 0
		}
	case "G":
		if m.workflow.selectedRunID == "" {
			if total > 0 {
				m.workflow.selectedIndex = total - 1
			}
		} else if run := m.selectedRunForWorkflow(); run != nil {
			rows := m.renderWorkflowTreeRows(run, 80)
			if len(rows) > 0 {
				m.workflow.scrollY = len(rows) - 1
			}
		}
	case "enter", "o":
		if m.workflow.selectedRunID == "" {
			// select a run from the selector list
			if m.workflow.selectedIndex >= 0 && m.workflow.selectedIndex < total {
				run := m.workflow.runs[m.workflow.selectedIndex]
				m.workflow.selectedRunID = run.ID
				m.workflow.scrollY = 0
			}
		} else {
			// toggle TODO expand + set selectedTodoID
			m = m.cycleWorkflowTodoExpand()
			// Set selectedTodoID to the TODO at current scroll position
			run := m.selectedRunForWorkflow()
			if run != nil {
				visible := 0
				for _, t := range run.Todos {
					if t.ParentID == "" || m.workflow.expandedTodo[t.ParentID] {
						if visible == m.workflow.scrollY {
							m.workflow.selectedTodoID = t.ID
							break
						}
						visible++
					}
				}
			}
		}
	case "r":
		// routing editor: only when no run is selected
		if m.workflow.selectedRunID == "" {
			m.workflow.showRoutingEditor = true
			m.workflow.routingEditTag = ""
			m.workflow.routingEditProfile = ""
			m.workflow.routingEditIndex = 0
			m.workflow.routingEditMode = false
			if m.workflow.routingDraft == nil {
				m.workflow.routingDraft = m.loadDriveRoutingFromProjectConfig()
				if m.workflow.routingDraft == nil {
					m.workflow.routingDraft = make(map[string]string)
				}
			}
		}
	case "esc":
		if m.workflow.showRoutingEditor {
			m.workflow.showRoutingEditor = false
		} else if m.workflow.selectedTodoID != "" {
			// deselect TODO — hide detail
			m.workflow.selectedTodoID = ""
		} else if m.workflow.selectedRunID != "" {
			// deselect run — back to run selector
			m.workflow.selectedRunID = ""
			m.workflow.scrollY = 0
		} else if m.workflow.followLive {
			m.workflow.followLive = false
			m.notice = "Workflow: live-follow off."
		}
	case " ", "space":
		// Toggle live-follow: when ON, the cursor + scroll auto-jump to
		// the currently-running TODO (or the latest active run if none
		// is running). Mirrors the Activity tab's tail-follow affordance
		// so users can keep eyes on the cockpit while a drive run
		// progresses without manual navigation.
		m.workflow.followLive = !m.workflow.followLive
		if m.workflow.followLive {
			m = m.snapWorkflowToLiveTarget()
			m.notice = "Workflow: live-follow ON — cursor tracks the running TODO. space to release."
		} else {
			m.notice = "Workflow: live-follow off."
		}
	}
	if m.workflow.selectedRunID != "" {
		if run := m.selectedRunForWorkflow(); run != nil {
			m.workflow.scrollY = clampScroll(m.workflow.scrollY, len(m.renderWorkflowTreeRows(run, 80)))
		} else {
			m.workflow.scrollY = 0
		}
	}
	return m, nil
}

// snapWorkflowToLiveTarget moves the cursor and scroll to whatever the
// run is actively executing right now: the running TODO if there is one,
// otherwise the active run in the run list. No-op when nothing is
// running. Pure state mutation — no event publishing — so it's safe to
// call from key handlers and from the engine event router.
func (m Model) snapWorkflowToLiveTarget() Model {
	// Prefer a TODO currently running inside the selected run.
	if run := m.selectedRunForWorkflow(); run != nil {
		visible := 0
		for _, t := range run.Todos {
			parentExpanded := t.ParentID == "" || m.workflow.expandedTodo[t.ParentID]
			if !parentExpanded {
				continue
			}
			if t.Status == drive.TodoRunning {
				m.workflow.scrollY = visible
				m.workflow.selectedTodoID = t.ID
				return m
			}
			visible++
		}
	}
	// Fall back to the most recently started running run in the list.
	for i, r := range m.workflow.runs {
		if r.Status == drive.RunRunning {
			m.workflow.selectedIndex = i
			return m
		}
	}
	return m
}

// cycleWorkflowTodoExpand finds the TODO at the current scroll position
// and toggles its expanded state.
func (m Model) cycleWorkflowTodoExpand() Model {
	run := m.selectedRunForWorkflow()
	if run == nil || len(run.Todos) == 0 {
		return m
	}
	// find Nth visible TODO at current scroll
	visible := 0
	var targetID string
	for _, t := range run.Todos {
		if t.ParentID == "" || m.workflow.expandedTodo[t.ParentID] {
			if visible == m.workflow.scrollY {
				targetID = t.ID
				break
			}
			visible++
		}
	}
	if targetID != "" {
		// Lazy-init: workflowPanelState is constructed as a zero value
		// in NewModel, so expandedTodo is nil until the first toggle.
		// Reads from a nil map are safe; writes panic with
		// "assignment to entry in nil map" — exactly the crash a user
		// hit pressing enter on the Workflow tab.
		if m.workflow.expandedTodo == nil {
			m.workflow.expandedTodo = make(map[string]bool)
		}
		m.workflow.expandedTodo[targetID] = !m.workflow.expandedTodo[targetID]
	}
	return m
}
