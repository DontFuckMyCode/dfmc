package tui

// provider_panel_key_list.go — list-view keyboard surface for the
// providers panel: the openProvidersActionMenu arrow-driven action
// menu builder (mirrors the p/f/m/s/n/r/d single-letter shortcuts so
// users don't have to memorise them) and handleProvidersListKey, the
// list-mode key dispatcher with j/k/pgup/pgdown/g/G navigation,
// search/clear, primary/fallback toggles, model cycle, save, detail
// view, new-provider entry, and refresh.
//
// Sibling of provider_panel_key.go which keeps the root dispatcher
// (handleProvidersKey routing confirmation/menu/search/input modes
// before falling through to the view-mode switch), provider search
// input, and the detail-view key handler. Other mode handlers
// (model picker, pipeline browse/draft, new-provider draft, profile
// edit) live in provider_panel_key_picker.go and
// provider_panel_key_edit.go.

import (
	tea "github.com/charmbracelet/bubbletea"
)

// openProvidersActionMenu — arrow-driven discovery for the Providers
// list. Mirrors the existing single-letter shortcut surface
// (p/f/m/s/n/r/d) so the user doesn't have to memorise them.
func (m Model) openProvidersActionMenu() Model {
	scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
	hasRow := scroll >= 0 && scroll < len(m.providers.rows)
	rowName := ""
	if hasRow {
		rowName = m.providers.rows[scroll].Name
	}

	actions := []panelAction{}
	if hasRow {
		actions = append(actions,
			panelAction{Label: "View detail · " + rowName, Accel: "d",
				Handler: func(m Model) (Model, tea.Cmd) {
					m.providers.detailProvider = rowName
					m.providers.viewMode = "detail"
					m.providers.scroll = 0
					m.notice = "viewing " + rowName
					return m, nil
				}},
			panelAction{Label: "Set " + rowName + " as primary", Accel: "p",
				Handler: func(m Model) (Model, tea.Cmd) {
					m = m.setPrimaryProvider(rowName)
					m.notice = rowName + " set as primary"
					return m, nil
				}},
			panelAction{Label: "Toggle " + rowName + " in fallback chain", Accel: "f",
				Handler: func(m Model) (Model, tea.Cmd) {
					m = m.toggleFallbackProvider(rowName)
					return m, nil
				}},
			panelAction{Label: "Cycle model for " + rowName, Accel: "m",
				Handler: func(m Model) (Model, tea.Cmd) {
					m = m.cycleProviderModel(rowName)
					if m.providers.err == "" {
						m.notice = rowName + " model cycled"
					}
					return m, nil
				}},
			panelAction{Label: "Save " + rowName + " model to user config", Accel: "s",
				Handler: func(m Model) (Model, tea.Cmd) {
					model := m.providers.rows[scroll].Model
					path, err := m.persistProviderModelUserConfig(rowName, model)
					if err != nil {
						m.notice = "save failed: " + err.Error()
					} else {
						m.notice = "saved → " + displayConfigPath(path)
					}
					return m, nil
				}},
			panelAction{Label: "Test connection · " + rowName, Accel: "T",
				Handler: func(m Model) (Model, tea.Cmd) {
					return m.startProviderProbe(rowName)
				}},
		)
	}
	actions = append(actions,
		panelAction{Label: "Add new provider", Accel: "n",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.providers.viewMode = "new_provider"
				m.providers.newProviderDraft = ""
				m.notice = "new provider — type name and enter"
				return m, nil
			}},
		panelAction{Label: "Sync provider profiles from models.dev", Accel: "y",
			Handler: func(m Model) (Model, tea.Cmd) {
				// Phase I item 3 — same async path the legacy menu had,
				// surfaced through the arrow-driven action menu so the
				// user doesn't need to memorise the bare-key.
				next, cmd := m.executeMenuAction("sync_models")
				if mm, ok := next.(Model); ok {
					return mm, cmd
				}
				return m, cmd
			}},
		panelAction{Label: "Refresh provider list", Accel: "r",
			Handler: func(m Model) (Model, tea.Cmd) {
				m = m.refreshProvidersRows()
				m.providers.loaded = true
				m.notice = "providers refreshed"
				return m, nil
			}},
		panelAction{Label: "Search providers…", Accel: "/",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.providers.searchActive = true
				return m, nil
			}},
		panelAction{Label: "Clear search query", Accel: "c",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.providers.query = ""
				m.providers.scroll = 0
				return m, nil
			}},
	)
	title := "Providers actions"
	if rowName != "" {
		title = "Actions · " + rowName
	}
	return m.openActionMenu("Providers", title, actions)
}

func (m Model) handleProvidersListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	if s := msg.String(); s == "right" || s == "l" {
		return m.openProvidersActionMenu(), nil
	}
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
	case "p":
		// Set selected provider as primary
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			m = m.setPrimaryProvider(name)
			m.notice = name + " set as primary"
		}
	case "f":
		// Toggle fallback membership
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			m = m.toggleFallbackProvider(name)
		}
	case "m":
		// Cycle model for selected provider
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			m = m.cycleProviderModel(name)
			if m.providers.err == "" {
				m.notice = name + " model cycled"
			}
		}
	case "s":
		// Save config for selected provider
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			model := m.providers.rows[scroll].Model
			path, err := m.persistProviderModelUserConfig(name, model)
			if err != nil {
				m.notice = "save failed: " + err.Error()
			} else {
				m.notice = "saved → " + displayConfigPath(path)
			}
		}
	case "T":
		// Phase I item 1 — probe the highlighted provider with a tiny
		// no-op completion. Result lands as a per-row chip via the
		// providerProbeMsg handler.
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			return m.startProviderProbe(name)
		}
	case "d", "enter":
		// View detail (enter also opens menu for power users)
		scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
		if scroll >= 0 && scroll < len(m.providers.rows) {
			name := m.providers.rows[scroll].Name
			m.providers.detailProvider = name
			m.providers.viewMode = "detail"
			m.providers.scroll = 0
			m.notice = "viewing " + name
		}
	case "n":
		// New provider
		m.providers.viewMode = "new_provider"
		m.providers.newProviderDraft = ""
		m.notice = "new provider — type name and enter"
	case "r":
		// Refresh provider list
		m = m.refreshProvidersRows()
		m.providers.loaded = true
		m.notice = "providers refreshed"
	default:
		// For anything else, open the action menu (backwards compat for
		// users used to pressing enter for the menu)
		if msg.Type == tea.KeyEnter {
			goto openMenu
		}
		return m, nil
	}
	return m, nil

openMenu:
	labels, actions, disabled, reasons := m.buildListMenu()
	if len(labels) > 0 {
		m.providers.menuActive = true
		m.providers.menuLabels = labels
		m.providers.menuActions = actions
		m.providers.menuDisabled = disabled
		m.providers.menuDisabledReasons = reasons
		m.providers.menuIndex = 0
	}
	return m, nil
}
