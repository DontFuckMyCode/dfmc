package tui

// plans.go — the Plans panel exposes the deterministic task decomposer
// in internal/planning. It's a "would this query fan out?" diagnostic:
// the user types a task, we run SplitTask against it, and render the
// subtasks with their split hint, the parallel/serial verdict, and a
// confidence meter.
//
// Shape: a query string, a cached Plan, a scroll offset into the subtask
// list, and an edit-mode flag that routes keypresses into the input.
// Computation is offline (pattern matching in planning/splitter.go), so
// there's no async load — Enter commits the query and renders in-place.
//
// Sibling: plans_render.go owns the visual primitives (confidence
// label/bar, subtask row, preview body), the top banner, and the
// renderPlansView orchestrator. This file keeps the state shape
// constants, the SplitTask runner, the arrow-only action menu, and
// the keyboard router for both view-mode and input-mode.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/planning"
)

const (
	// plansDescriptionChars caps the preview body so a pasted multi-line
	// query can't push the rest of the view off-screen.
	plansDescriptionChars = 600
)

// runPlansSplit computes the plan for the current query and stamps it
// into the model. Pure function — no engine round-trip.
func (m Model) runPlansSplit() Model {
	q := strings.TrimSpace(m.plans.query)
	if q == "" {
		m.plans.plan = nil
		m.plans.err = "task is empty"
		return m
	}
	m.plans.err = ""
	p := planning.SplitTask(q)
	m.plans.plan = &p
	m.plans.scroll = 0
	return m
}

// openPlansActionMenu — arrow-driven discovery for Plans.
func (m Model) openPlansActionMenu() Model {
	actions := []panelAction{
		{Label: "Edit task (opens text input)", Accel: "e",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.plans.inputActive = true
				return m, nil
			}},
		{Label: "Re-run split with current task",
			Handler: func(m Model) (Model, tea.Cmd) {
				if strings.TrimSpace(m.plans.query) != "" {
					m = m.runPlansSplit()
				}
				return m, nil
			}},
		{Label: "Clear task and result", Accel: "c",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.plans.query = ""
				m.plans.plan = nil
				m.plans.err = ""
				m.plans.scroll = 0
				return m, nil
			}},
	}
	return m.openActionMenu("Plans", "Plans actions", actions)
}

func (m Model) handlePlansKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.plans.inputActive {
		return m.handlePlansInputKey(msg)
	}
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	if s := msg.String(); s == "right" || s == "l" {
		return m.openPlansActionMenu(), nil
	}
	total := 0
	if m.plans.plan != nil {
		total = len(m.plans.plan.Subtasks)
	}
	step := 1
	pageStep := 10
	switch msg.String() {
	case "e":
		m.plans.inputActive = true
		return m, nil
	case "j", "down":
		if m.plans.scroll+step < total {
			m.plans.scroll += step
		}
	case "k", "up":
		if m.plans.scroll >= step {
			m.plans.scroll -= step
		} else {
			m.plans.scroll = 0
		}
	case "pgdown":
		if m.plans.scroll+pageStep < total {
			m.plans.scroll += pageStep
		} else if total > 0 {
			m.plans.scroll = total - 1
		}
	case "pgup":
		if m.plans.scroll >= pageStep {
			m.plans.scroll -= pageStep
		} else {
			m.plans.scroll = 0
		}
	case "g":
		m.plans.scroll = 0
	case "G":
		if total > 0 {
			m.plans.scroll = total - 1
		}
	case "c":
		m.plans.query = ""
		m.plans.plan = nil
		m.plans.err = ""
		m.plans.scroll = 0
	case "enter":
		// Re-run with the current query — cheap, and gives the user a
		// way to reload without editing.
		if strings.TrimSpace(m.plans.query) != "" {
			m = m.runPlansSplit()
		}
	}
	return m, nil
}

func (m Model) handlePlansInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.plans.inputActive = false
		m = m.runPlansSplit()
		return m, nil
	case tea.KeyEsc:
		m.plans.inputActive = false
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.plans.query); len(r) > 0 {
			m.plans.query = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.plans.query += msg.String()
		return m, nil
	}
	return m, nil
}
