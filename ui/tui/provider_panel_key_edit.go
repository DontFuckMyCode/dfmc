package tui

// provider_panel_key_edit.go — text-entry edit modes for the providers
// panel: pipeline draft (name + step provider/model fields), new
// provider name, and the four-field profile editor. Each handler
// owns its own commit-on-enter / cancel-on-esc flow and routes back
// to the list view on completion. The dispatcher + browse keys live
// in provider_panel_key.go; pickers live in provider_panel_key_picker.go.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func (m Model) handlePipelineEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	steps := m.providers.pipelineDraftSteps
	stepIdx := m.providers.pipelineEditStep
	field := m.providers.pipelineEditField

	switch msg.String() {
	case "esc":
		m.providers.pipelineEditMode = false
		m.providers.pipelineDraftName = ""
		m.providers.pipelineDraftSteps = nil
		m.providers.pipelineDraftBuf = ""
		m.notice = "pipeline edit cancelled"
	case "enter":
		if stepIdx == len(steps) {
			// "+ Add Step" pseudo-row selected
			m.providers.pipelineDraftSteps = append(steps, config.PipelineStep{})
			m.providers.pipelineEditStep = len(steps)
			m.providers.pipelineEditField = 0
			m.providers.pipelineDraftBuf = ""
			return m, nil
		}
		if stepIdx == -1 {
			// name field active, move to first step
			if m.providers.pipelineDraftBuf != "" {
				m.providers.pipelineDraftName = m.providers.pipelineDraftBuf
				m.providers.pipelineDraftBuf = ""
			}
			if len(steps) > 0 {
				m.providers.pipelineEditStep = 0
				m.providers.pipelineEditField = 0
			} else {
				// no steps, save immediately
				if err := m.savePipelineDraft(); err != nil {
					m.notice = "save failed: " + err.Error()
				}
			}
			return m, nil
		}
		// commit current field buffer
		if stepIdx >= 0 && stepIdx < len(steps) {
			if field == 0 {
				steps[stepIdx].Provider = m.providers.pipelineDraftBuf
			} else {
				steps[stepIdx].Model = m.providers.pipelineDraftBuf
			}
			m.providers.pipelineDraftBuf = ""
		}
		// if last field of last step, save; else next field
		if stepIdx == len(steps)-1 && field == 1 {
			if err := m.savePipelineDraft(); err != nil {
				m.notice = "save failed: " + err.Error()
			}
		} else if field == 0 {
			m.providers.pipelineEditField = 1
		} else {
			m.providers.pipelineEditStep = stepIdx + 1
			m.providers.pipelineEditField = 0
		}
	case "tab":
		if stepIdx == -1 {
			if m.providers.pipelineDraftBuf != "" {
				m.providers.pipelineDraftName = m.providers.pipelineDraftBuf
				m.providers.pipelineDraftBuf = ""
			}
			if len(steps) > 0 {
				m.providers.pipelineEditStep = 0
				m.providers.pipelineEditField = 0
			} else {
				// no steps yet, jump to add row
				m.providers.pipelineEditStep = 0
				m.providers.pipelineEditField = 0
			}
			return m, nil
		}
		if stepIdx >= 0 && stepIdx < len(steps) {
			if m.providers.pipelineDraftBuf != "" {
				if field == 0 {
					steps[stepIdx].Provider = m.providers.pipelineDraftBuf
				} else {
					steps[stepIdx].Model = m.providers.pipelineDraftBuf
				}
				m.providers.pipelineDraftBuf = ""
			}
			if field == 0 {
				m.providers.pipelineEditField = 1
			} else if stepIdx < len(steps)-1 {
				m.providers.pipelineEditStep = stepIdx + 1
				m.providers.pipelineEditField = 0
			} else {
				// last field of last step → add row
				m.providers.pipelineEditStep = len(steps)
				m.providers.pipelineEditField = 0
			}
		}
	case "down":
		if stepIdx < len(steps) {
			m.providers.pipelineEditStep = stepIdx + 1
			m.providers.pipelineEditField = 0
			m.providers.pipelineDraftBuf = ""
		}
	case "up":
		if stepIdx > -1 {
			m.providers.pipelineEditStep = stepIdx - 1
			m.providers.pipelineEditField = 0
			m.providers.pipelineDraftBuf = ""
		}
	case "backspace":
		if r := []rune(m.providers.pipelineDraftBuf); len(r) > 0 {
			m.providers.pipelineDraftBuf = string(r[:len(r)-1])
		} else if stepIdx >= 0 && stepIdx < len(steps) {
			// delete current step when buffer is empty
			m.providers.pipelineDraftSteps = append(steps[:stepIdx], steps[stepIdx+1:]...)
			if stepIdx >= len(m.providers.pipelineDraftSteps) {
				m.providers.pipelineEditStep = len(m.providers.pipelineDraftSteps) - 1
			}
			if m.providers.pipelineEditStep < -1 {
				m.providers.pipelineEditStep = -1
			}
			m.providers.pipelineEditField = 0
		}
	default:
		// typing into active field
		if msg.Type == tea.KeySpace {
			m.providers.pipelineDraftBuf += " "
		} else if msg.Type == tea.KeyRunes {
			m.providers.pipelineDraftBuf += string(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) handleNewProviderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m = m.closeProviderTextEdit()
		m.providers.viewMode = "list"
		m.providers.newProviderDraft = ""
		m.notice = "new provider cancelled"
	case "enter":
		name := strings.TrimSpace(m.providers.newProviderDraft)
		if name == "" {
			return m.beginProviderTextEdit("new_provider", 0, "Provider name", "", false), nil
		}
		if err := m.createProvider(name); err != nil {
			m.notice = "create failed: " + err.Error()
		} else {
			m.providers.newProviderDraft = ""
			m.providers.viewMode = "detail"
			m.providers.detailProvider = name
			m = m.refreshProvidersRows()
			m = m.focusProviderRow(name)
			m.notice = "created provider: " + name
		}
	case "backspace":
		m.notice = "press Enter to edit provider name"
	default:
		if textKeyMsg(msg) {
			m.notice = "press Enter before typing or pasting"
		}
	}
	return m, nil
}

func (m Model) handleProfileEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.providers.profileEditField == 0 {
		switch msg.String() {
		case "enter", "right", "space":
			m = m.cycleProfileProtocol(1)
			return m, nil
		case "left":
			m = m.cycleProfileProtocol(-1)
			return m, nil
		}
	}
	switch msg.String() {
	case "esc":
		m = m.closeProviderTextEdit()
		m.providers.profileEditMode = false
		m.providers.profileEditDraft = ""
		m.notice = "profile edit cancelled"
	case "enter":
		return m.openProfileEditFieldEditor()
	case "tab":
		m.providers.profileEditField = (m.providers.profileEditField + 1) % 3
		m.providers.profileEditDraft = ""
	case "down":
		if m.providers.profileEditField < 2 {
			m.providers.profileEditField++
		}
	case "up":
		if m.providers.profileEditField > 0 {
			m.providers.profileEditField--
		}
	case "backspace":
		m.notice = "press Enter on a field to edit it"
	default:
		if textKeyMsg(msg) {
			m.notice = "press Enter on a field before typing or pasting"
		}
	}
	return m, nil
}

func (m Model) openProfileEditFieldEditor() (tea.Model, tea.Cmd) {
	title := "Protocol"
	value := m.profileEditFieldValue(m.providers.profileEditField)
	secret := false
	switch m.providers.profileEditField {
	case 0:
		m = m.cycleProfileProtocol(1)
		return m, nil
	case 1:
		title = "Endpoint"
	case 2:
		title = "API key"
		secret = true
	}
	return m.beginProviderTextEdit("profile_edit", m.providers.profileEditField, title, value, secret), nil
}

func (m Model) cycleProfileProtocol(delta int) Model {
	if m.eng == nil || m.eng.Config == nil {
		return m
	}
	prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
	if !ok {
		m.notice = "provider not found"
		return m
	}
	current := strings.TrimSpace(prof.Protocol)
	idx := 0
	for i, option := range providerCompatibleOptions {
		if strings.EqualFold(option, current) {
			idx = i
			break
		}
	}
	idx += delta
	for idx < 0 {
		idx += len(providerCompatibleOptions)
	}
	idx %= len(providerCompatibleOptions)
	prof.Protocol = providerCompatibleOptions[idx]
	m.eng.Config.Providers.Profiles[m.providers.detailProvider] = prof
	if err := m.persistProfileEdits(); err != nil {
		m.notice = "protocol save failed: " + err.Error()
		return m
	}
	m = m.refreshProvidersRows()
	m = m.focusProviderRow(m.providers.detailProvider)
	m.status = m.eng.Status()
	m.notice = "protocol -> " + nonEmpty(prof.Protocol, "(auto)") + " (cycled)"
	return m
}

func (m Model) profileEditFieldValue(field int) string {
	if m.eng == nil || m.eng.Config == nil {
		return ""
	}
	prof := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
	switch field {
	case 0:
		return prof.Protocol
	case 1:
		return prof.BaseURL
	case 2:
		return prof.APIKey
	default:
		return ""
	}
}
