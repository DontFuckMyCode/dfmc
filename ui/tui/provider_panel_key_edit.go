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
	case "j", "down":
		if stepIdx < len(steps) {
			m.providers.pipelineEditStep = stepIdx + 1
			m.providers.pipelineEditField = 0
			m.providers.pipelineDraftBuf = ""
		}
	case "k", "up":
		if stepIdx > -1 {
			m.providers.pipelineEditStep = stepIdx - 1
			m.providers.pipelineEditField = 0
			m.providers.pipelineDraftBuf = ""
		}
	case "d":
		if stepIdx >= 0 && stepIdx < len(steps) {
			m.providers.pipelineDraftSteps = append(steps[:stepIdx], steps[stepIdx+1:]...)
			if stepIdx >= len(m.providers.pipelineDraftSteps) {
				m.providers.pipelineEditStep = len(m.providers.pipelineDraftSteps) - 1
			}
			if m.providers.pipelineEditStep < -1 {
				m.providers.pipelineEditStep = -1
			}
			m.providers.pipelineEditField = 0
		}
	case "backspace":
		if len(m.providers.pipelineDraftBuf) > 0 {
			m.providers.pipelineDraftBuf = m.providers.pipelineDraftBuf[:len(m.providers.pipelineDraftBuf)-1]
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
		if msg.Type == tea.KeyRunes {
			m.providers.pipelineDraftBuf += string(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) handleNewProviderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.providers.viewMode = "list"
		m.providers.newProviderDraft = ""
		m.notice = "new provider cancelled"
	case "enter":
		name := strings.TrimSpace(m.providers.newProviderDraft)
		if name == "" {
			m.notice = "provider name is required"
			return m, nil
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
		if len(m.providers.newProviderDraft) > 0 {
			m.providers.newProviderDraft = m.providers.newProviderDraft[:len(m.providers.newProviderDraft)-1]
		}
	default:
		if msg.Type == tea.KeyRunes {
			m.providers.newProviderDraft += string(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) handleProfileEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.providers.profileEditMode = false
		m.providers.profileEditDraft = ""
		m.notice = "profile edit cancelled"
	case "enter":
		m.commitProfileEditField()
		if err := m.persistProfileEdits(); err != nil {
			m.notice = "save failed: " + err.Error()
		} else {
			m = m.refreshProvidersRows()
			m = m.focusProviderRow(m.providers.detailProvider)
			m.notice = "saved profile for " + m.providers.detailProvider
		}
		m.providers.profileEditMode = false
		m.providers.profileEditDraft = ""
	case "tab":
		m.commitProfileEditField()
		m.providers.profileEditField = (m.providers.profileEditField + 1) % 4
		m.providers.profileEditDraft = ""
	case "backspace":
		if len(m.providers.profileEditDraft) > 0 {
			m.providers.profileEditDraft = m.providers.profileEditDraft[:len(m.providers.profileEditDraft)-1]
		}
	default:
		if msg.Type == tea.KeyRunes {
			m.providers.profileEditDraft += string(msg.Runes)
		}
	}
	return m, nil
}
