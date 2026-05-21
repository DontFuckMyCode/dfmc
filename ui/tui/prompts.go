package tui

// prompts.go — the Prompts panel is a read view over internal/promptlib:
// the full template library after defaults, ~/.dfmc/prompts, and
// .dfmc/prompts overrides are merged. The goal is debug-by-inspection —
// users can see which template would win for a task/role combo and why,
// without having to re-run an Ask to watch the bundle rendering code.
//
// This file owns the load command, key dispatch, and arrow-driven
// action menu. Rendering (filteredPrompts, formatPromptRow,
// formatPromptPreview, wrapPromptLines, nonEmpty, renderPromptsView,
// promptsTopBanner) lives in prompts_render.go.
//
// Shape: a list of promptlib.Template, a search query, a scroll offset,
// and an optional preview of the currently highlighted body. Because
// promptlib instances are built ad-hoc at every call site in DFMC (the
// CLI and Web do the same), this panel also builds its own on refresh —
// which means it always reflects what a *fresh* render would see.

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

const (
	// promptsPreviewChars caps the rendered body length so one monster
	// template can't eat the whole panel.
	promptsPreviewChars = 4000
)

type promptsLoadedMsg struct {
	templates []promptlib.Template
	err       error
}

func loadPromptsCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		lib := promptlib.New()
		// Project root may be blank in degraded-startup mode — that's fine,
		// LoadOverrides just skips the empty root.
		root := ""
		if eng != nil {
			root = eng.ProjectRoot
		}
		if err := lib.LoadOverrides(root); err != nil {
			return promptsLoadedMsg{err: err}
		}
		return promptsLoadedMsg{templates: lib.List()}
	}
}

// filteredPrompts narrows the list with a case-insensitive substring
// match over ID / Type / Task / Role / Language / Profile / Description.
// Body is deliberately excluded — it's long and matching it would make
// every "what do I have?" query look like a full-text search.

// openPromptsActionMenu — arrow-driven action surface for Prompts.
// Enter still loads the preview; Right opens the menu.
func (m Model) openPromptsActionMenu() Model {
	actions := []panelAction{
		{Label: "Load preview",
			Handler: func(m Model) (Model, tea.Cmd) {
				filtered := filteredPrompts(m.prompts.templates, m.prompts.query)
				if len(filtered) == 0 || m.prompts.scroll < 0 || m.prompts.scroll >= len(filtered) {
					return m, nil
				}
				m.prompts.previewID = filtered[m.prompts.scroll].ID
				return m, nil
			}},
		{Label: "Refresh templates", Accel: "r",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.prompts.loading = true
				m.prompts.err = ""
				return m, loadPromptsCmd(m.eng)
			}},
		{Label: "Search…", Accel: "/",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.prompts.searchActive = true
				return m, nil
			}},
		{Label: "Clear search query", Accel: "c",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.prompts.query = ""
				m.prompts.scroll = 0
				return m, nil
			}},
	}
	return m.openActionMenu("Prompts", "Prompt actions", actions)
}

func (m Model) handlePromptsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.prompts.searchActive {
		return m.handlePromptsSearchKey(msg)
	}
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	if s := msg.String(); s == "right" || s == "l" {
		return m.openPromptsActionMenu(), nil
	}
	total := len(filteredPrompts(m.prompts.templates, m.prompts.query))
	step := 1
	pageStep := 10
	switch msg.String() {
	case "j", "down":
		if m.prompts.scroll+step < total {
			m.prompts.scroll += step
		}
	case "k", "up":
		if m.prompts.scroll >= step {
			m.prompts.scroll -= step
		} else {
			m.prompts.scroll = 0
		}
	case "pgdown":
		if m.prompts.scroll+pageStep < total {
			m.prompts.scroll += pageStep
		} else if total > 0 {
			m.prompts.scroll = total - 1
		}
	case "pgup":
		if m.prompts.scroll >= pageStep {
			m.prompts.scroll -= pageStep
		} else {
			m.prompts.scroll = 0
		}
	case "g":
		m.prompts.scroll = 0
	case "G":
		if total > 0 {
			m.prompts.scroll = total - 1
		}
	case "enter":
		filtered := filteredPrompts(m.prompts.templates, m.prompts.query)
		if len(filtered) == 0 || m.prompts.scroll < 0 || m.prompts.scroll >= len(filtered) {
			return m, nil
		}
		m.prompts.previewID = filtered[m.prompts.scroll].ID
		return m, nil
	case "r":
		m.prompts.loading = true
		m.prompts.err = ""
		return m, loadPromptsCmd(m.eng)
	case "/":
		m.prompts.searchActive = true
		return m, nil
	case "c":
		m.prompts.query = ""
		m.prompts.scroll = 0
	}
	return m, nil
}

func (m Model) handlePromptsSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.prompts.searchActive = false
		m.prompts.scroll = 0
		return m, nil
	case tea.KeyEsc:
		m.prompts.searchActive = false
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.prompts.query); len(r) > 0 {
			m.prompts.query = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.prompts.query += msg.String()
		return m, nil
	}
	return m, nil
}
