package tui

import tea "github.com/charmbracelet/bubbletea"

func (m Model) openProvidersActionMenu() Model {
	row, hasRow := m.selectedProviderRow()
	actions := []panelAction{}
	if hasRow {
		rowName := row.Name
		actions = append(actions,
			panelAction{Label: "Open details for " + rowName, Handler: func(m Model) (Model, tea.Cmd) {
				m.providers.detailProvider = rowName
				m.providers.viewMode = "detail"
				m.providers.modelEditIdx = 0
				m.providers.modelQuery = ""
				m.providers.modelSearchActive = false
				m.notice = "viewing " + rowName
				return m, nil
			}},
			panelAction{Label: "Make " + rowName + " primary for chat", Handler: func(m Model) (Model, tea.Cmd) {
				m = m.setPrimaryProvider(rowName)
				m.notice = rowName + " is primary; next prompt uses this provider"
				return m, nil
			}},
			panelAction{Label: "Test connection", Handler: func(m Model) (Model, tea.Cmd) {
				return m.startProviderProbe(rowName)
			}},
			panelAction{Label: "Delete provider", Handler: func(m Model) (Model, tea.Cmd) {
				m.providers.confirmAction = "delete_provider"
				m.providers.confirmTarget = rowName
				return m, nil
			}},
		)
	}
	actions = append(actions,
		panelAction{Label: "Sync provider catalog from models.dev", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.syncing = true
			m.providers.catalogLoaded = false
			return m, syncModelsDevCmd(m.eng)
		}},
		panelAction{Label: "Add provider from catalog", Handler: func(m Model) (Model, tea.Cmd) {
			m = m.loadProviderCatalogItems()
			m.providers.viewMode = providerViewCatalog
			return m, nil
		}},
		panelAction{Label: "Add custom provider", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.viewMode = "new_provider"
			m.providers.newProviderDraft = ""
			m.notice = "type provider name, then Enter"
			return m, nil
		}},
		panelAction{Label: "Open tier matrix", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.viewMode = providerViewTiers
			return m, nil
		}},
		panelAction{Label: "Open skill model routes", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.viewMode = providerViewSkills
			return m, nil
		}},
		panelAction{Label: "Reset all saved keys", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.confirmAction = "reset_all_keys"
			m.providers.confirmTarget = "all providers"
			return m, nil
		}},
		panelAction{Label: "Refresh my providers", Handler: func(m Model) (Model, tea.Cmd) {
			m = m.refreshProvidersRows()
			m.providers.loaded = true
			m.notice = "providers refreshed"
			return m, nil
		}},
		panelAction{Label: "Search providers", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.searchActive = true
			return m, nil
		}},
	)
	title := "Provider actions"
	if hasRow {
		title = "Actions - " + row.Name
	}
	return m.openActionMenu("Providers", title, actions)
}

func (m Model) handleProvidersListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	rows := m.visibleProviderRows()
	total := len(rows)
	switch msg.String() {
	case "down":
		if m.providers.scroll+1 < total {
			m.providers.scroll++
		}
	case "up":
		if m.providers.scroll > 0 {
			m.providers.scroll--
		}
	case "pgdown":
		if m.providers.scroll+10 < total {
			m.providers.scroll += 10
		} else if total > 0 {
			m.providers.scroll = total - 1
		}
	case "pgup":
		if m.providers.scroll >= 10 {
			m.providers.scroll -= 10
		} else {
			m.providers.scroll = 0
		}
	case "home":
		m.providers.scroll = 0
	case "end":
		if total > 0 {
			m.providers.scroll = total - 1
		}
	case "ctrl+f", "/":
		m.providers.searchActive = true
	case "enter", "right", "space":
		return m.openProvidersActionMenu(), nil
	}
	return m, nil
}
