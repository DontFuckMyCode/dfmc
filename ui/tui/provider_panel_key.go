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
		if m.providers.textEditActive {
			return m.handleProviderTextEditKey(msg)
		}
		if nm, handled := m.handleProviderInputTextKey(msg); handled {
			return nm, nil
		}
		switch m.providers.viewMode {
		case providerViewCatalogForm:
			return m.handleCatalogProviderFormKey(msg)
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

	switch m.providers.viewMode {
	case "detail":
		if m.providers.modelPickerActive && !m.providers.modelPickerManual {
			return m.handleModelPickerKey(msg)
		}
		return m.handleProvidersDetailKey(msg)
	case providerViewCatalog:
		return m.handleProviderCatalogKey(msg)
	case providerViewTiers:
		return m.handleProviderTiersKey(msg)
	case providerViewSkills:
		return m.handleProviderSkillsKey(msg)
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
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	if m.providers.modelSearchActive {
		return m.handleProviderModelSearchKey(msg)
	}
	switch msg.String() {
	case "esc", "left":
		m.providers.viewMode = "list"
		m.providers.detailProvider = ""
		m.providers.modelEditIdx = 0
		m.providers.modelQuery = ""
		m.providers.modelSearchActive = false
	case "/", "ctrl+f":
		m.providers.modelSearchActive = true
		m.notice = "model search: type to filter, enter keeps result, esc clears"
	case "down":
		models := m.detailProviderVisibleModels()
		if len(models) > 0 && m.providers.modelEditIdx+1 < len(models) {
			m.providers.modelEditIdx++
		}
	case "up":
		models := m.detailProviderVisibleModels()
		if len(models) > 0 && m.providers.modelEditIdx > 0 {
			m.providers.modelEditIdx--
		}
	case "home":
		models := m.detailProviderVisibleModels()
		if len(models) > 0 {
			m.providers.modelEditIdx = 0
		}
	case "end":
		models := m.detailProviderVisibleModels()
		if len(models) > 0 {
			m.providers.modelEditIdx = len(models) - 1
		}
	case "pgdown":
		models := m.detailProviderVisibleModels()
		if len(models) > 0 {
			m.providers.modelEditIdx += 12
			if m.providers.modelEditIdx >= len(models) {
				m.providers.modelEditIdx = len(models) - 1
			}
		}
	case "pgup":
		models := m.detailProviderVisibleModels()
		if len(models) > 0 {
			m.providers.modelEditIdx -= 12
			if m.providers.modelEditIdx < 0 {
				m.providers.modelEditIdx = 0
			}
		}
	case "enter", "right", "space":
		return m.openProviderDetailActionMenu(), nil
	}
	return m, nil
}

func (m Model) handleProviderModelSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	models := m.detailProviderVisibleModels()
	switch msg.Type {
	case tea.KeyEsc:
		m.providers.modelSearchActive = false
		m.providers.modelQuery = ""
		m.providers.modelEditIdx = 0
		m.notice = "model search cleared"
		return m, nil
	case tea.KeyEnter:
		m.providers.modelSearchActive = false
		m.providers.modelEditIdx = clampScroll(m.providers.modelEditIdx, len(models))
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.providers.modelQuery); len(r) > 0 {
			m.providers.modelQuery = string(r[:len(r)-1])
		}
		m.providers.modelEditIdx = clampScroll(m.providers.modelEditIdx, len(m.detailProviderVisibleModels()))
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		if msg.Type == tea.KeySpace {
			m.providers.modelQuery += " "
		} else {
			m.providers.modelQuery += string(msg.Runes)
		}
		m.providers.modelEditIdx = 0
		return m, nil
	}
	switch msg.String() {
	case "ctrl+u":
		m.providers.modelQuery = ""
		m.providers.modelEditIdx = 0
		return m, nil
	case "down":
		if len(models) > 0 && m.providers.modelEditIdx+1 < len(models) {
			m.providers.modelEditIdx++
		}
	case "up":
		if len(models) > 0 && m.providers.modelEditIdx > 0 {
			m.providers.modelEditIdx--
		}
	case "home":
		m.providers.modelEditIdx = 0
	case "end":
		if len(models) > 0 {
			m.providers.modelEditIdx = len(models) - 1
		}
	case "pgdown":
		if len(models) > 0 {
			m.providers.modelEditIdx += 12
			if m.providers.modelEditIdx >= len(models) {
				m.providers.modelEditIdx = len(models) - 1
			}
		}
	case "pgup":
		m.providers.modelEditIdx -= 12
		if m.providers.modelEditIdx < 0 {
			m.providers.modelEditIdx = 0
		}
	}
	return m, nil
}
