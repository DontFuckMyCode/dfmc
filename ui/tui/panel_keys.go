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
	// Action menu has priority — when open it owns arrows/enter/esc.
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
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
	case "enter", "right", "l":
		// Enter (or Right) opens the action menu — discoverable path
		// for users who don't know p/r/i/e/v. Single-letter
		// accelerators below stay registered for power users.
		return m.openFilesActionMenu(), loadFilePreviewCmd(m.eng, m.selectedFile())
	case "p", "alt+p":
		return m.togglePinnedFile()
	case "i", "e", "v":
		return m.insertFileIntoComposer(msg.String())
	}
	return m, nil
}

// openFilesActionMenu builds the contextual action list for the
// currently-selected file row and opens the menu. Pin/Unpin label
// flips based on whether the row is already pinned so the user sees
// the actual outcome.
func (m Model) openFilesActionMenu() Model {
	path := m.selectedFile()
	if path == "" {
		return m
	}
	pinned := strings.TrimSpace(m.filesView.pinned) == path
	pinLabel := "Pin to chat context"
	if pinned {
		pinLabel = "Unpin from chat context"
	}
	actions := []panelAction{
		{Label: "Open preview", Accel: "enter", Handler: func(m Model) (Model, tea.Cmd) {
			return m, loadFilePreviewCmd(m.eng, m.selectedFile())
		}},
		{Label: pinLabel, Accel: "p", Handler: func(m Model) (Model, tea.Cmd) {
			nm, cmd := m.togglePinnedFile()
			if mm, ok := nm.(Model); ok {
				return mm, cmd
			}
			return m, cmd
		}},
		{Label: "Insert [[file:…]] into chat", Accel: "i", Handler: func(m Model) (Model, tea.Cmd) {
			return m.insertFileIntoComposer("i")
		}},
		{Label: "Explain this file in chat", Accel: "e", Handler: func(m Model) (Model, tea.Cmd) {
			return m.insertFileIntoComposer("e")
		}},
		{Label: "Review this file in chat", Accel: "v", Handler: func(m Model) (Model, tea.Cmd) {
			return m.insertFileIntoComposer("v")
		}},
		{Label: "Reload index", Accel: "r", Handler: func(m Model) (Model, tea.Cmd) {
			return m, loadFilesCmd(m.eng)
		}},
	}
	return m.openActionMenu("Files", "Actions for "+truncateForLine(path, 32), actions)
}

// insertFileIntoComposer handles i/e/v keys in the Files panel.
// Each key builds a different prompt prefix but all follow the same
// pattern: get selected file → switch to Chat tab → set chat input.
func (m Model) insertFileIntoComposer(key string) (Model, tea.Cmd) {
	path := m.selectedFile()
	if path == "" {
		return m, nil
	}
	prefix := map[string]string{
		"i": fmt.Sprintf("[[file:%s]]", path),
		"e": fmt.Sprintf("Explain [[file:%s]] ", path),
		"v": fmt.Sprintf("Review [[file:%s]] ", path),
	}[key]
	current := strings.TrimRight(m.chat.input, " ")
	if current != "" {
		current += " "
	}
	m.setChatInput(current + prefix)
	m.activeTab = 0
	m.notice = "Switched to Chat with " + prefix + "."
	return m, nil
}

// openToolsActionMenu — arrow-driven discovery for the Tools tab.
// Run / edit / reset / rerun all reachable from one menu so the user
// doesn't memorise enter/r/e/x.
func (m Model) openToolsActionMenu() Model {
	tools := m.availableTools()
	if len(tools) == 0 {
		return m
	}
	if m.toolView.index < 0 || m.toolView.index >= len(tools) {
		m.toolView.index = 0
	}
	name := tools[m.toolView.index]
	actions := []panelAction{
		{Label: "Run with current params", Accel: "enter",
			Handler: func(m Model) (Model, tea.Cmd) {
				params, err := m.toolPresetParams(name)
				if err != nil {
					m.toolView.output = fmt.Sprintf("Tool: %s\nStatus: blocked\n\n%s", name, err.Error())
					m.notice = "tool preset: " + err.Error()
					return m, nil
				}
				m.notice = "Running tool: " + name
				return m, runToolCmd(m.ctx, m.eng, name, params)
			}},
		{Label: "Edit params (opens param editor)", Accel: "enter",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.toolView.editing = true
				m.toolView.draft = m.toolPresetSummary(name)
				m.notice = "Editing params for " + name
				return m, nil
			}},
		{Label: "Reset params to default", Accel: "enter",
			Handler: func(m Model) (Model, tea.Cmd) {
				if m.toolView.overrides != nil {
					delete(m.toolView.overrides, name)
				}
				m.toolView.draft = ""
				m.notice = "Reset params for " + name
				return m, nil
			}},
		{Label: "Re-run last invocation", Accel: "enter",
			Handler: func(m Model) (Model, tea.Cmd) {
				params, err := m.toolPresetParams(name)
				if err != nil {
					m.toolView.output = fmt.Sprintf("Tool: %s\nStatus: blocked\n\n%s", name, err.Error())
					return m, nil
				}
				m.notice = "Re-running tool: " + name
				return m, runToolCmd(m.ctx, m.eng, name, params)
			}},
	}
	// Toggle enable/disable (label changes based on current state).
	if m.eng != nil {
		isDisabled := m.eng.IsToolDisabled(name)
		isProtected := m.eng.ToolIsProtected(name)
		if isDisabled {
			actions = append(actions, panelAction{
				Label: "Enable tool", Accel: "enter",
				Handler: func(m Model) (Model, tea.Cmd) {
					if err := m.eng.SetToolEnabled(name, true); err != nil {
						m.notice = "enable failed: " + err.Error()
						return m, nil
					}
					m.notice = name + " enabled"
					return m, nil
				},
			})
		} else if !isProtected {
			actions = append(actions, panelAction{
				Label: "Disable tool", Accel: "enter",
				Handler: func(m Model) (Model, tea.Cmd) {
					if err := m.eng.SetToolEnabled(name, false); err != nil {
						m.notice = "disable failed: " + err.Error()
						return m, nil
					}
					m.notice = name + " disabled"
					return m, nil
				},
			})
		}
	}
	return m.openActionMenu("Tools", "Actions for "+name, actions)
}

func (m Model) handleToolsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	tools := m.availableTools()
	if len(tools) == 0 {
		m.notice = "No tools registered."
		return m, nil
	}
	if !m.toolView.editing {
		if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
			return nm, cmd
		}
		if msg.String() == "right" {
			return m.openToolsActionMenu(), nil
		}
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
	case "down":
		if m.toolView.index < len(tools)-1 {
			m.toolView.index++
		}
		m.notice = "Tool selection: " + tools[m.toolView.index]
		return m, nil
	case "up":
		if m.toolView.index > 0 {
			m.toolView.index--
		}
		m.notice = "Tool selection: " + tools[m.toolView.index]
		return m, nil
	case "enter":
		name := tools[m.toolView.index]
		if m.eng != nil && m.eng.IsToolDisabled(name) {
			m.notice = name + " is disabled — press → to open action menu"
			return m, nil
		}
		params, err := m.toolPresetParams(name)
		if err != nil {
			m.toolView.output = fmt.Sprintf("Tool: %s\nStatus: blocked\n\n%s", name, err.Error())
			m.notice = "tool preset: " + err.Error()
			return m, nil
		}
		m.notice = "Running tool: " + name
		return m, runToolCmd(m.ctx, m.eng, name, params)
	case "right":
		return m.openToolsActionMenu(), nil
	}
	return m, nil
}
	
