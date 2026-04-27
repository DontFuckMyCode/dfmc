// Per-panel key handlers. Update dispatches panel-specific keys
// (handled here) after the global chord layer misses. Extracted from
// tui.go — each handler owns the Files/Tools panel's own keyboard UX
// with no cross-panel traffic.

package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleFilesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r", "alt+r":
		return m, loadFilesCmd(m.eng)
	case "down", "j", "alt+j":
		if len(m.filesView.entries) == 0 {
			return m, nil
		}
		if m.filesView.index < len(m.filesView.entries)-1 {
			m.filesView.index++
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	case "up", "k", "alt+k":
		if len(m.filesView.entries) == 0 {
			return m, nil
		}
		if m.filesView.index > 0 {
			m.filesView.index--
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	case "enter":
		if len(m.filesView.entries) == 0 {
			return m, nil
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	case "p", "alt+p":
		return m.togglePinnedFile()
	}
	return m, nil
}

func (m Model) handleToolsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	tools := m.availableTools()
	if len(tools) == 0 {
		m.notice = "No tools registered."
		return m, nil
	}
	m.toolView.index = clampIndex(m.toolView.index, len(tools))
	if m.toolView.editing {
		switch msg.Type {
		case tea.KeyRunes:
			m.toolView.draft += string(msg.Runes)
			return m, nil
		case tea.KeySpace:
			m.toolView.draft += " "
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			runes := []rune(m.toolView.draft)
			if len(runes) > 0 {
				m.toolView.draft = string(runes[:len(runes)-1])
			}
			return m, nil
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
			return m, nil
		case tea.KeyEsc:
			m.toolView.editing = false
			m.notice = "Tool edit cancelled."
			return m, nil
		}
		return m, nil
	}
	switch msg.String() {
	case "down", "j", "alt+j":
		if m.toolView.index < len(tools)-1 {
			m.toolView.index++
		}
		m.notice = "Tool selection: " + tools[m.toolView.index]
		return m, nil
	case "up", "k", "alt+k":
		if m.toolView.index > 0 {
			m.toolView.index--
		}
		m.notice = "Tool selection: " + tools[m.toolView.index]
		return m, nil
	case "e", "alt+e":
		name := tools[m.toolView.index]
		m.toolView.editing = true
		m.toolView.draft = m.toolPresetSummary(name)
		m.notice = "Editing params for " + name
		return m, nil
	case "x", "alt+x":
		name := tools[m.toolView.index]
		if m.toolView.overrides != nil {
			delete(m.toolView.overrides, name)
		}
		m.toolView.draft = ""
		m.notice = "Reset params for " + name
		return m, nil
	case "enter", "r", "alt+r":
		name := tools[m.toolView.index]
		params, err := m.toolPresetParams(name)
		if err != nil {
			m.toolView.output = fmt.Sprintf("Tool: %s\nStatus: blocked\n\n%s", name, err.Error())
			m.notice = "tool preset: " + err.Error()
			return m, nil
		}
		m.notice = "Running tool: " + name
		return m, runToolCmd(m.ctx, m.eng, name, params)
	}
	return m, nil
}
