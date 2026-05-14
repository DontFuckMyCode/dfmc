package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) runSelectedTool(name string, rerun bool) (Model, tea.Cmd) {
	params, err := m.toolPresetParams(name)
	if err != nil {
		m.toolView.output = fmt.Sprintf("Tool: %s\nStatus: blocked\n\n%s", name, err.Error())
		m.notice = "tool preset: " + err.Error()
		return m, nil
	}
	if rerun {
		m.notice = "Re-running tool: " + name
	} else {
		m.notice = "Running tool: " + name
	}
	return m, runToolCmd(m.ctx, m.eng, name, params)
}

func (m Model) startToolParamEdit(name string) (Model, tea.Cmd) {
	m.toolView.editing = true
	m.toolView.draft = m.toolPresetSummary(name)
	m.notice = "Editing params for " + name
	return m, nil
}

func (m Model) resetToolParams(name string) (Model, tea.Cmd) {
	if m.toolView.overrides != nil {
		delete(m.toolView.overrides, name)
	}
	m.toolView.draft = ""
	m.notice = "Reset params for " + name
	return m, nil
}

func (m Model) setToolEnabled(name string, enabled bool) (Model, tea.Cmd) {
	if m.eng == nil {
		return m, nil
	}
	if err := m.eng.SetToolEnabled(name, enabled); err != nil {
		if enabled {
			m.notice = "enable failed: " + err.Error()
		} else {
			m.notice = "disable failed: " + err.Error()
		}
		return m, nil
	}
	if enabled {
		m.notice = name + " enabled"
	} else {
		m.notice = name + " disabled"
	}
	return m, nil
}

func (m Model) handleToolEditKey(msg tea.KeyMsg, tools []string) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyRunes:
		m.toolView.draft += string(msg.Runes)
	case tea.KeySpace:
		m.toolView.draft += " "
	case tea.KeyBackspace, tea.KeyCtrlH:
		runes := []rune(m.toolView.draft)
		if len(runes) > 0 {
			m.toolView.draft = string(runes[:len(runes)-1])
		}
	case tea.KeyEnter:
		name := tools[m.toolView.index]
		if m.toolView.overrides == nil {
			m.toolView.overrides = map[string]string{}
		}
		trimmed := strings.TrimSpace(m.toolView.draft)
		if trimmed == "" {
			delete(m.toolView.overrides, name)
			m.notice = "Tool params reset: " + name
		} else {
			m.toolView.overrides[name] = trimmed
			m.notice = "Tool params saved: " + name
		}
		m.toolView.editing = false
	case tea.KeyEsc:
		m.toolView.editing = false
		m.notice = "Tool edit cancelled."
	}
	return m, nil
}

func (m Model) handleToolSelectionKey(msg tea.KeyMsg, tools []string) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "down":
		if m.toolView.index < len(tools)-1 {
			m.toolView.index++
		}
		m.notice = "Tool selection: " + tools[m.toolView.index]
	case "up":
		if m.toolView.index > 0 {
			m.toolView.index--
		}
		m.notice = "Tool selection: " + tools[m.toolView.index]
	case "enter":
		name := tools[m.toolView.index]
		if m.eng != nil && m.eng.IsToolDisabled(name) {
			m.notice = name + " is disabled - press right to open action menu"
			return m, nil
		}
		return m.runSelectedTool(name, false)
	case "e", "alt+e":
		return m.startToolParamEdit(tools[m.toolView.index])
	case "x":
		return m.resetToolParams(tools[m.toolView.index])
	case "r", "alt+r":
		return m.runSelectedTool(tools[m.toolView.index], true)
	case "right":
		return m.openToolsActionMenu(), nil
	}
	return m, nil
}
