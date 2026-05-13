package tui

// provider_panel_key_picker.go - pickers and the stats-panel provider
// chip handler. Provider panel pickers use arrow/home/end/page keys,
// Enter to commit, Esc to cancel, and Space only where the local
// surface needs a mode switch.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleModelPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.providers.modelPickerManual {
		switch msg.String() {
		case "esc":
			m.providers.modelPickerActive = false
			m.providers.modelPickerDraft = ""
		case "enter":
			draft := strings.TrimSpace(m.providers.modelPickerDraft)
			if draft != "" {
				m.addModelToProvider(m.providers.detailProvider, draft)
			}
			m.providers.modelPickerActive = false
			m.providers.modelPickerDraft = ""
		case "backspace":
			if r := []rune(m.providers.modelPickerDraft); len(r) > 0 {
				m.providers.modelPickerDraft = string(r[:len(r)-1])
			}
		default:
			if msg.Type == tea.KeySpace {
				m.providers.modelPickerDraft += " "
			} else if msg.Type == tea.KeyRunes {
				m.providers.modelPickerDraft += string(msg.Runes)
			}
		}
		return m, nil
	}

	if msg.Type == tea.KeySpace {
		m.providers.modelPickerManual = true
		m.providers.modelPickerDraft = ""
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.providers.modelPickerActive = false
		m.providers.modelPickerDraft = ""
	case "enter":
		items := m.providers.modelPickerItems
		if m.providers.modelPickerIndex >= 0 && m.providers.modelPickerIndex < len(items) {
			m.addModelToProvider(m.providers.detailProvider, items[m.providers.modelPickerIndex])
		}
		m.providers.modelPickerActive = false
	case "down":
		if m.providers.modelPickerIndex+1 < len(m.providers.modelPickerItems) {
			m.providers.modelPickerIndex++
		}
	case "up":
		if m.providers.modelPickerIndex > 0 {
			m.providers.modelPickerIndex--
		}
	case "home":
		m.providers.modelPickerIndex = 0
	case "end":
		if len(m.providers.modelPickerItems) > 0 {
			m.providers.modelPickerIndex = len(m.providers.modelPickerItems) - 1
		}
	case "pgdown":
		m.providers.modelPickerIndex += 12
		if total := len(m.providers.modelPickerItems); total > 0 && m.providers.modelPickerIndex >= total {
			m.providers.modelPickerIndex = total - 1
		}
	case "pgup":
		m.providers.modelPickerIndex -= 12
		if m.providers.modelPickerIndex < 0 {
			m.providers.modelPickerIndex = 0
		}
	}
	return m, nil
}

func (m Model) handleProvidersPipelineKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.providers.pipelineEditMode {
		return m.handlePipelineEditKey(msg)
	}
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}

	names := m.providers.pipelineNames
	total := len(names)
	switch msg.String() {
	case "esc", "left":
		m.providers.viewMode = "list"
	case "down":
		if m.providers.pipelineScroll+1 < total {
			m.providers.pipelineScroll++
		}
	case "up":
		if m.providers.pipelineScroll > 0 {
			m.providers.pipelineScroll--
		}
	case "home":
		m.providers.pipelineScroll = 0
	case "end":
		if total > 0 {
			m.providers.pipelineScroll = total - 1
		}
	case "pgdown":
		if m.providers.pipelineScroll+10 < total {
			m.providers.pipelineScroll += 10
		} else if total > 0 {
			m.providers.pipelineScroll = total - 1
		}
	case "pgup":
		if m.providers.pipelineScroll >= 10 {
			m.providers.pipelineScroll -= 10
		} else {
			m.providers.pipelineScroll = 0
		}
	case "enter", "right", "space":
		return m.openProvidersPipelineActionMenu(), nil
	}
	return m, nil
}

func (m Model) handleStatsPanelProviderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	panelRows := m.providerStatusPanelRows()
	if len(panelRows) == 0 {
		return m, nil
	}

	total := len(panelRows)
	m.providers.selectedIndex = clampScroll(m.providers.selectedIndex, total)
	step := 1

	switch msg.String() {
	case "j", "down":
		if m.providers.editMode == "" {
			if m.providers.selectedIndex+step < total {
				m.providers.selectedIndex += step
			}
		}
	case "k", "up":
		if m.providers.editMode == "" {
			if m.providers.selectedIndex >= step {
				m.providers.selectedIndex -= step
			} else {
				m.providers.selectedIndex = 0
			}
		}
	case "g":
		if m.providers.editMode == "" {
			m.providers.selectedIndex = 0
		}
	case "G":
		if m.providers.editMode == "" && total > 0 {
			m.providers.selectedIndex = total - 1
		}
	case "enter":
		if m.providers.selectedIndex >= 0 && m.providers.selectedIndex < total {
			row := panelRows[m.providers.selectedIndex]
			if m.providers.editMode == "model" {
				// commit model edit
				if len(row.Models) > 0 {
					model := row.Models[m.providers.modelEditIdx]
					m = m.applyProviderModelSelection(row.Name, model)
				}
				m.providers.editMode = ""
			} else if m.providers.editMode == "fallback" {
				// commit fallback edit
				m.providers.editMode = ""
				m.notice = "fallback profile for " + row.Name + " updated"
			} else {
				// switch to this provider (use best model)
				model := ""
				if len(row.Models) > 0 {
					model = row.Models[0]
				}
				m = m.applyProviderModelSelection(row.Name, model)
			}
		}
	case "m":
		if m.providers.selectedIndex >= 0 && m.providers.selectedIndex < total {
			row := panelRows[m.providers.selectedIndex]
			if len(row.Models) > 1 {
				if m.providers.editMode == "model" {
					m.providers.modelEditIdx = (m.providers.modelEditIdx + 1) % len(row.Models)
				} else {
					m.providers.editMode = "model"
					m.providers.modelEditIdx = 0
				}
			} else {
				m.notice = row.Name + " has no additional models to cycle"
			}
		}
	case "f":
		if m.providers.selectedIndex >= 0 && m.providers.selectedIndex < total {
			row := panelRows[m.providers.selectedIndex]
			if len(row.FallbackModels) > 0 {
				if m.providers.editMode == "fallback" {
					m.providers.fallbackIdx = (m.providers.fallbackIdx + 1) % len(row.FallbackModels)
				} else {
					m.providers.editMode = "fallback"
					m.providers.fallbackIdx = 0
				}
			} else {
				m.notice = row.Name + " has no fallback models configured"
			}
		}
	case "s":
		if m.providers.selectedIndex >= 0 && m.providers.selectedIndex < total {
			row := panelRows[m.providers.selectedIndex]
			model := ""
			if len(row.Models) > 0 {
				model = row.Models[0]
			}
			path, err := m.persistProviderModelUserConfig(row.Name, model)
			if err != nil {
				m.notice = "save failed: " + err.Error()
			} else {
				m.notice = "saved → " + displayConfigPath(path)
			}
		}
	}
	return m, nil
}
