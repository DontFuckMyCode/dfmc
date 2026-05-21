package tui

import tea "github.com/charmbracelet/bubbletea"

func (m Model) handleProvidersConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		action := m.providers.confirmAction
		target := m.providers.confirmTarget
		m.providers.confirmAction = ""
		m.providers.confirmTarget = ""
		return m.executeConfirmedProviderAction(action, target)
	case "esc":
		m.notice = "Cancelled."
		m.providers.confirmAction = ""
		m.providers.confirmTarget = ""
	}
	return m, nil
}

func (m Model) executeConfirmedProviderAction(action, target string) (tea.Model, tea.Cmd) {
	switch action {
	case "delete_provider":
		if err := m.deleteProvider(target); err != nil {
			m.notice = "Delete failed: " + err.Error()
		} else {
			m = m.refreshProvidersRows()
			m.notice = "Deleted provider: " + target
		}
	case "delete_model":
		m = m.deleteActiveModel()
	case "delete_pipeline":
		if err := m.deletePipeline(target); err != nil {
			m.notice = "Delete failed: " + err.Error()
		} else {
			m.providers.pipelineNames = m.pipelineNamesFromEngine()
			m.providers.pipelineScroll = clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
			m.notice = "Deleted pipeline: " + target
		}
	case "reset_all_keys":
		path, err := m.clearAllProviderKeys()
		if err != nil {
			m.notice = "Reset keys failed: " + err.Error()
		} else {
			m = m.refreshProvidersRows()
			m.notice = "All provider keys removed from " + displayConfigPath(path)
		}
	}
	return m, nil
}
