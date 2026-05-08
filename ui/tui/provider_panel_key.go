package tui

// provider_panel_key.go — root keyboard dispatcher for the providers
// panel + the three browse-mode handlers (list, search, detail).
// The remaining mode-specific handlers live in siblings:
//
//   provider_panel_key_picker.go  — model picker, pipeline browse,
//                                   stats-panel provider chip
//   provider_panel_key_edit.go    — pipeline draft, new provider,
//                                   profile editor
//
// The dispatcher's job is twofold: route confirmation/menu/search/
// input modes to the right handler before falling through to the
// view-mode switch, and own the always-active right-arrow shortcut
// that opens the contextual action menu.

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleProvidersKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Confirmation mode takes priority over everything
	if m.providers.confirmAction != "" {
		return m.handleProvidersConfirmKey(msg)
	}

	// Input modes (typing) bypass menus and global shortcuts
	if isProvidersInputMode(m) {
		switch m.providers.viewMode {
		case "new_provider":
			return m.handleNewProviderKey(msg)
		case "pipelines":
			return m.handlePipelineEditKey(msg)
		case "detail":
			if m.providers.profileEditMode {
				return m.handleProfileEditKey(msg)
			}
			if m.providers.modelPickerManual {
				return m.handleModelPickerKey(msg)
			}
		}
		return m, nil
	}

	if m.providers.searchActive {
		return m.handleProvidersSearchKey(msg)
	}

	if m.providers.menuActive {
		return m.handleProvidersMenuKey(msg)
	}

	switch m.providers.viewMode {
	case "detail":
		if m.providers.modelPickerActive && !m.providers.modelPickerManual {
			return m.handleModelPickerKey(msg)
		}
		return m.handleProvidersDetailKey(msg)
	case "pipelines":
		return m.handleProvidersPipelineKey(msg)
	case "new_provider":
		return m.handleNewProviderKey(msg)
	default:
		return m.handleProvidersListKey(msg)
	}
}

// openProvidersActionMenu + handleProvidersListKey live in
// provider_panel_key_list.go.

func (m Model) handleProvidersSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.providers.searchActive = false
		m.providers.scroll = 0
		return m, nil
	case tea.KeyEsc:
		m.providers.searchActive = false
		m.providers.query = ""
		m.providers.scroll = 0
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.providers.query); len(r) > 0 {
			m.providers.query = string(r[:len(r)-1])
		}
		m.providers.scroll = 0
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.providers.query += msg.String()
		m.providers.scroll = 0
		return m, nil
	}
	return m, nil
}

func (m Model) handleProvidersDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.providers.viewMode = "list"
		m.providers.detailProvider = ""
		m.providers.modelEditIdx = 0
	case "j", "down":
		models := m.detailProviderModels()
		if len(models) > 0 && m.providers.modelEditIdx+1 < len(models) {
			m.providers.modelEditIdx++
		}
	case "k", "up":
		models := m.detailProviderModels()
		if len(models) > 0 && m.providers.modelEditIdx > 0 {
			m.providers.modelEditIdx--
		}
	case "g", "home":
		models := m.detailProviderModels()
		if len(models) > 0 {
			m.providers.modelEditIdx = 0
		}
	case "G", "end":
		models := m.detailProviderModels()
		if len(models) > 0 {
			m.providers.modelEditIdx = len(models) - 1
		}
	case "pgdown":
		models := m.detailProviderModels()
		if len(models) > 0 {
			m.providers.modelEditIdx += 12
			if m.providers.modelEditIdx >= len(models) {
				m.providers.modelEditIdx = len(models) - 1
			}
		}
	case "pgup":
		models := m.detailProviderModels()
		if len(models) > 0 {
			m.providers.modelEditIdx -= 12
			if m.providers.modelEditIdx < 0 {
				m.providers.modelEditIdx = 0
			}
		}
	case "enter":
		labels, actions, disabled, reasons := m.buildDetailMenu()
		if len(labels) > 0 {
			m.providers.menuActive = true
			m.providers.menuLabels = labels
			m.providers.menuActions = actions
			m.providers.menuDisabled = disabled
			m.providers.menuDisabledReasons = reasons
			m.providers.menuIndex = 0
		}
	}
	return m, nil
}
