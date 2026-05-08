package tui

// provider_panel_key_picker.go — pickers and the stats-panel provider
// chip handler. Three keyboard surfaces live here because they share
// the same shape: a list-with-cursor where j/k/g/G/pgup/pgdown move,
// enter commits, esc cancels, and m cycles. The text-entry edit modes
// (new provider / profile edit / pipeline edit) live in
// provider_panel_key_edit.go; the dispatcher + list/search/detail keys
// live in provider_panel_key.go.

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
			if len(m.providers.modelPickerDraft) > 0 {
				m.providers.modelPickerDraft = m.providers.modelPickerDraft[:len(m.providers.modelPickerDraft)-1]
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.providers.modelPickerDraft += string(msg.Runes)
			}
		}
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
	case "j", "down":
		if m.providers.modelPickerIndex+1 < len(m.providers.modelPickerItems) {
			m.providers.modelPickerIndex++
		}
	case "k", "up":
		if m.providers.modelPickerIndex > 0 {
			m.providers.modelPickerIndex--
		}
	case "g", "home":
		m.providers.modelPickerIndex = 0
	case "G", "end":
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
	case "m":
		m.providers.modelPickerManual = true
		m.providers.modelPickerDraft = ""
	}
	return m, nil
}

func (m Model) handleProvidersPipelineKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.providers.pipelineEditMode {
		return m.handlePipelineEditKey(msg)
	}

	names := m.providers.pipelineNames
	total := len(names)
	switch msg.String() {
	case "esc", "q":
		m.providers.viewMode = "list"
	case "j", "down":
		if m.providers.pipelineScroll+1 < total {
			m.providers.pipelineScroll++
		}
	case "k", "up":
		if m.providers.pipelineScroll > 0 {
			m.providers.pipelineScroll--
		}
	case "g", "home":
		m.providers.pipelineScroll = 0
	case "G", "end":
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
	case "enter":
		labels, actions, disabled, reasons := m.buildPipelineMenu()
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

func (m Model) handleStatsPanelProviderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ensure rows are populated
	panelRows := m.providerPanelRows()
	if len(panelRows) == 0 {
		return m, nil
	}

	total := len(panelRows)
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
