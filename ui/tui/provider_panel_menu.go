package tui

// provider_panel_menu.go — the action side of the providers panel.
//
// provider_panel_key.go owns the per-mode key handlers (list/detail/
// picker/pipeline/stats/new-provider). When one of those handlers
// pops the contextual menu, the menu's own key handler + the confirm
// dialog + the action dispatcher live here. Splitting them out keeps
// the key-routing file focused on "given the current mode and this
// keystroke, what's next?" while the menu file is "given this chosen
// action string, what does it actually do?" — separate domains that
// were growing in the same file.

import (
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dontfuckmycode/dfmc/internal/config"
)

func (m Model) handleProvidersMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	total := len(m.providers.menuLabels)
	if total == 0 {
		m.providers.menuActive = false
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.providers.menuActive = false
	case "j", "down":
		m.providers.menuIndex = nextEnabledMenuIndex(m.providers.menuDisabled, m.providers.menuIndex, total, 1)
	case "k", "up":
		m.providers.menuIndex = nextEnabledMenuIndex(m.providers.menuDisabled, m.providers.menuIndex, total, -1)
	case "g":
		m.providers.menuIndex = nextEnabledMenuIndex(m.providers.menuDisabled, -1, total, 1)
	case "G":
		m.providers.menuIndex = nextEnabledMenuIndex(m.providers.menuDisabled, total, total, -1)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		n, _ := strconv.Atoi(msg.String())
		if n > 0 && n <= total {
			idx := n - 1
			if idx < len(m.providers.menuDisabled) && m.providers.menuDisabled[idx] {
				m.notice = "that action is not available"
			} else {
				m.providers.menuIndex = idx
			}
		}
	case "enter":
		if m.providers.menuIndex < len(m.providers.menuDisabled) && m.providers.menuDisabled[m.providers.menuIndex] {
			m.notice = "that action is not available"
			return m, nil
		}
		action := m.providers.menuActions[m.providers.menuIndex]
		m.providers.menuActive = false
		return m.executeMenuAction(action)
	}
	return m, nil
}

func (m Model) handleProvidersConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		action := m.providers.confirmAction
		target := m.providers.confirmTarget
		m.providers.confirmAction = ""
		m.providers.confirmTarget = ""
		return m.executeConfirmedAction(action, target)
	case "n", "esc":
		m.notice = "cancelled"
		m.providers.confirmAction = ""
		m.providers.confirmTarget = ""
	}
	return m, nil
}

func (m Model) executeConfirmedAction(action, target string) (tea.Model, tea.Cmd) {
	switch action {
	case "delete_provider":
		if err := m.deleteProvider(target); err != nil {
			m.notice = "delete failed: " + err.Error()
		} else {
			m = m.refreshProvidersRows()
			m.notice = "deleted provider: " + target
		}
	case "delete_model":
		m = m.deleteActiveModel()
	case "delete_pipeline":
		if err := m.deletePipeline(target); err != nil {
			m.notice = "delete failed: " + err.Error()
		} else {
			m.providers.pipelineNames = m.pipelineNamesFromEngine()
			m.providers.pipelineScroll = clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
			m.notice = "deleted pipeline: " + target
		}
	}
	return m, nil
}

func (m Model) executeMenuAction(action string) (tea.Model, tea.Cmd) {
	switch action {
	case "detail":
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			m.providers.viewMode = "detail"
			m.providers.detailProvider = m.providers.rows[scroll].Name
			m.providers.modelEditIdx = 0
		}
	case "back":
		m.providers.viewMode = "list"
		m.providers.detailProvider = ""
	case "set_primary":
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			if m.eng != nil {
				m.eng.SetPrimaryProvider(name)
			}
			m = m.refreshProvidersRows()
			m = m.focusProviderRow(name)
			m.notice = "set primary: " + name
		}
	case "toggle_fallback":
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			m = m.toggleFallbackProvider(name)
		}
	case "cycle_model":
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			m = m.cycleProviderModel(name)
		}
	case "save_config":
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			model := m.providers.rows[scroll].Model
			path, err := m.persistProviderModelProjectConfig(name, model)
			if err != nil {
				m.notice = "save failed: " + err.Error()
			} else {
				m.notice = "saved " + path
			}
		}
	case "sync_models":
		m.providers.syncing = true
		return m, syncModelsDevCmd(m.eng)
	case "pipelines":
		m.providers.viewMode = "pipelines"
		m.providers.pipelineNames = m.pipelineNamesFromEngine()
		m.providers.pipelineScroll = 0
	case "new_provider":
		m.providers.viewMode = "new_provider"
		m.providers.newProviderDraft = ""
	case "delete_provider":
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			m.providers.confirmAction = "delete_provider"
			m.providers.confirmTarget = name
		}
	case "refresh":
		m = m.refreshProvidersRows()
	case "edit_profile":
		m.providers.profileEditMode = true
		m.providers.profileEditField = 0
		m.providers.profileEditDraft = ""
	case "add_model":
		m.providers.modelPickerActive = true
		m.providers.modelPickerManual = false
		m.providers.modelPickerIndex = 0
		m.providers.modelPickerDraft = ""
		if m.eng != nil {
			m.providers.modelPickerItems = m.loadModelsDevForProvider(m.providers.detailProvider)
		}
	case "set_active_model":
		if m.eng == nil || m.eng.Config == nil {
			m.notice = "engine not ready"
			return m, nil
		}
		prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
		if !ok {
			m.notice = "provider not found"
			return m, nil
		}
		models := prof.AllModels()
		if len(models) == 0 {
			m.notice = "no models to set"
			return m, nil
		}
		idx := m.providers.modelEditIdx
		if idx < 0 || idx >= len(models) {
			idx = 0
		}
		prof.Model = models[idx]
		m.eng.Config.Providers.Profiles[m.providers.detailProvider] = prof
		m = m.refreshProvidersRows()
		m = m.focusProviderRow(m.providers.detailProvider)
		m.notice = fmt.Sprintf("set active model → %s", prof.Model)
	case "delete_model":
		if m.eng == nil || m.eng.Config == nil {
			m.notice = "engine not ready"
			return m, nil
		}
		prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
		if !ok {
			m.notice = "provider not found"
			return m, nil
		}
		models := prof.AllModels()
		if len(models) == 0 {
			m.notice = "no models to delete"
			return m, nil
		}
		idx := m.providers.modelEditIdx
		if idx < 0 || idx >= len(models) {
			idx = 0
		}
		m.providers.confirmAction = "delete_model"
		m.providers.confirmTarget = models[idx]
	case "activate":
		scroll := clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
		if scroll >= 0 && scroll < len(m.providers.pipelineNames) {
			name := m.providers.pipelineNames[scroll]
			if m.eng != nil {
				if err := m.eng.ActivatePipeline(name); err != nil {
					m.notice = "pipeline failed: " + err.Error()
				} else {
					m.providers.activePipeline = name
					m.status = m.eng.Status()
					m.notice = "activated pipeline: " + name
				}
			}
		}
	case "edit":
		scroll := clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
		if scroll >= 0 && scroll < len(m.providers.pipelineNames) {
			name := m.providers.pipelineNames[scroll]
			if m.eng != nil {
				if pipe, ok := m.eng.Pipeline(name); ok {
					m.providers.pipelineEditMode = true
					m.providers.pipelineDraftName = name
					m.providers.pipelineDraftSteps = append([]config.PipelineStep(nil), pipe.Steps...)
					m.providers.pipelineEditStep = -1
					m.providers.pipelineEditField = 0
					m.providers.pipelineDraftBuf = ""
				}
			}
		}
	case "delete":
		scroll := clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
		if scroll >= 0 && scroll < len(m.providers.pipelineNames) {
			name := m.providers.pipelineNames[scroll]
			m.providers.confirmAction = "delete_pipeline"
			m.providers.confirmTarget = name
		}
	case "new":
		m.providers.pipelineEditMode = true
		m.providers.pipelineDraftName = ""
		m.providers.pipelineDraftSteps = nil
		m.providers.pipelineEditStep = -1
		m.providers.pipelineEditField = 0
		m.providers.pipelineDraftBuf = ""
	}
	return m, nil
}
