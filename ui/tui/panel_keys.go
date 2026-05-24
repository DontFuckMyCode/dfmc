package tui

// Per-panel key handlers. Update dispatches panel-specific keys after the
// global chord layer misses. Each handler owns the Files/Tools panel keyboard
// UX with no cross-panel traffic.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleFilesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filesView.searchActive {
		return m.handleFilesSearchKey(msg)
	}
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	visible := m.visibleFilesEntries()
	switch msg.String() {
	case "r", "alt+r":
		return m, loadFilesCmd(m.eng)
	case "down", "j", "alt+j":
		if len(visible) == 0 {
			return m, nil
		}
		m.filesView.index = clampScroll(m.filesView.index, len(visible))
		if m.filesView.index < len(visible)-1 {
			m.filesView.index++
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	case "up", "k", "alt+k":
		if len(visible) == 0 {
			return m, nil
		}
		m.filesView.index = clampScroll(m.filesView.index, len(visible))
		if m.filesView.index > 0 {
			m.filesView.index--
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	case "enter", "right", "l":
		return m.openFilesActionMenu(), loadFilePreviewCmd(m.eng, m.selectedFile())
	case "p", "alt+p":
		return m.togglePinnedFile()
	case "i", "e", "v":
		return m.insertFileIntoComposer(msg.String())
	case "/":
		m.filesView.searchActive = true
		return m, nil
	case "c", "alt+c":
		// Clear the active query so the next paint surfaces every
		// indexed file again. No-op when nothing is buffered.
		if strings.TrimSpace(m.filesView.query) != "" {
			m.filesView.query = ""
			m.filesView.index = 0
		}
		return m, nil
	}
	return m, nil
}

// handleFilesSearchKey is the focused search-input handler — same
// shape as handleCodemapSearchKey / handleMemorySearchKey. Enter
// commits and drops out, Esc cancels and drops out, every other key
// routes through applyInlineSearchTextKey so backspace + printable
// runes mutate filesView.query inline. The query stays applied even
// after commit (it's the filter) — commit just hands the keyboard
// back to the regular Files navigator.
func (m Model) handleFilesSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.filesView.searchActive = false
		m.filesView.index = 0
		return m, nil
	case tea.KeyEsc:
		m.filesView.searchActive = false
		return m, nil
	default:
		if query, ok := applyInlineSearchTextKey(m.filesView.query, msg); ok {
			m.filesView.query = query
		}
	}
	return m, nil
}

func (m Model) openFilesActionMenu() Model {
	path := m.selectedFile()
	if path == "" {
		return m
	}
	pinLabel := "Pin to chat context"
	if strings.TrimSpace(m.filesView.pinned) == path {
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
		{Label: "Insert [[file:...]] into chat", Accel: "i", Handler: func(m Model) (Model, tea.Cmd) {
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
		{Label: "Run with current params", Handler: func(m Model) (Model, tea.Cmd) {
			return m.runSelectedTool(name, false)
		}},
		{Label: "Edit params (opens param editor)", Accel: "e", Handler: func(m Model) (Model, tea.Cmd) {
			return m.startToolParamEdit(name)
		}},
		{Label: "Reset params to default", Accel: "x", Handler: func(m Model) (Model, tea.Cmd) {
			return m.resetToolParams(name)
		}},
		{Label: "Re-run last invocation", Accel: "r", Handler: func(m Model) (Model, tea.Cmd) {
			return m.runSelectedTool(name, true)
		}},
	}
	if m.eng != nil {
		isDisabled := m.eng.IsToolDisabled(name)
		isProtected := m.eng.ToolIsProtected(name)
		if isDisabled {
			actions = append(actions, panelAction{Label: "Enable tool", Accel: "d", Handler: func(m Model) (Model, tea.Cmd) {
				return m.setToolEnabled(name, true)
			}})
		} else if !isProtected {
			actions = append(actions, panelAction{Label: "Disable tool", Accel: "d", Handler: func(m Model) (Model, tea.Cmd) {
				return m.setToolEnabled(name, false)
			}})
		}
	}
	return m.openActionMenu("Tools", "Actions for "+name, actions)
}

func (m Model) handleToolsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.toolView.searchActive {
		return m.handleToolsSearchKey(msg)
	}
	// Search affordances must work even with an empty registry so the
	// keys stay discoverable; the action menu / selection handlers
	// require a non-empty list.
	if !m.toolView.editing {
		switch msg.String() {
		case "/":
			m.toolView.searchActive = true
			return m, nil
		case "c":
			if strings.TrimSpace(m.toolView.query) != "" {
				m.toolView.query = ""
				m.toolView.index = 0
				return m, nil
			}
		}
	}
	if len(m.availableTools()) == 0 {
		m.notice = "No tools registered."
		return m, nil
	}
	tools := m.visibleTools()
	if !m.toolView.editing {
		if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
			return nm, cmd
		}
		if msg.String() == "right" {
			return m.openToolsActionMenu(), nil
		}
	}
	if len(tools) == 0 {
		// All tools filtered out — let the user clear without crashing
		// the selection handlers that index into tools[].
		return m, nil
	}
	m.toolView.index = clampIndex(m.toolView.index, len(tools))
	if m.toolView.editing {
		return m.handleToolEditKey(msg, tools)
	}
	return m.handleToolSelectionKey(msg, tools)
}

// handleToolsSearchKey is the focused search-input handler for the
// Tools panel. Same shape as the Files / CodeMap / Memory equivalents:
// Enter commits, Esc cancels, runes/backspace edit the buffer inline.
// The query stays applied after commit — the filter is the point.
func (m Model) handleToolsSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.toolView.searchActive = false
		m.toolView.index = 0
		return m, nil
	case tea.KeyEsc:
		m.toolView.searchActive = false
		return m, nil
	default:
		if query, ok := applyInlineSearchTextKey(m.toolView.query, msg); ok {
			m.toolView.query = query
		}
	}
	return m, nil
}
