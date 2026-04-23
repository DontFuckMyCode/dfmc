package tui

// provider_panel_key.go — per-mode keyboard routers for the providers
// panel (list / search / detail / model picker / pipeline / stats /
// new-provider / profile-edit / pipeline-edit). The menu + confirm
// dialog + executeMenuAction / executeConfirmedAction dispatchers
// moved into provider_panel_menu.go — this file stays focused on
// "given the mode and this keystroke, where does it go?" routing.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dontfuckmycode/dfmc/internal/config"
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

func (m Model) handleProvidersListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtered := filteredProviderRows(m.providers.rows, m.providers.query)
	total := len(filtered)
	step := 1
	pageStep := 10

	switch msg.String() {
	case "j", "down":
		if m.providers.scroll+step < total {
			m.providers.scroll += step
		}
	case "k", "up":
		if m.providers.scroll >= step {
			m.providers.scroll -= step
		} else {
			m.providers.scroll = 0
		}
	case "pgdown":
		if m.providers.scroll+pageStep < total {
			m.providers.scroll += pageStep
		} else if total > 0 {
			m.providers.scroll = total - 1
		}
	case "pgup":
		if m.providers.scroll >= pageStep {
			m.providers.scroll -= pageStep
		} else {
			m.providers.scroll = 0
		}
	case "g", "home":
		m.providers.scroll = 0
	case "G", "end":
		if total > 0 {
			m.providers.scroll = total - 1
		}
	case "/":
		m.providers.searchActive = true
	case "c":
		m.providers.query = ""
		m.providers.scroll = 0
	case "enter":
		labels, actions, disabled, reasons := m.buildListMenu()
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
					m.notice = formatProviderSwitchNotice(m.providerProfile(row.Name))
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
				m.notice = formatProviderSwitchNotice(m.providerProfile(row.Name))
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
			path, err := m.persistProviderModelProjectConfig(row.Name, model)
			if err != nil {
				m.notice = "save failed: " + err.Error()
			} else {
				m.notice = "saved " + path
			}
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

