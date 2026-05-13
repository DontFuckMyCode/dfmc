package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) openProviderDetailActionMenu() Model {
	name := strings.TrimSpace(m.providers.detailProvider)
	if name == "" {
		return m
	}
	actions := []panelAction{
		{Label: "Use selected model for this session", Handler: func(m Model) (Model, tea.Cmd) {
			model, ok := m.selectedDetailModel()
			if !ok {
				m.notice = "no model selected"
				return m, nil
			}
			if m.eng != nil {
				if m.eng.Config != nil {
					prof := m.eng.Config.Providers.Profiles[name]
					prof.Model = model
					prof = applyCatalogModelLimits(prof, model)
					m.eng.Config.Providers.Profiles[name] = prof
				}
				m.eng.SetProviderModel(name, model)
				m.status = m.eng.Status()
			}
			m.notice = fmt.Sprintf("session model -> %s:%s", name, model)
			return m, nil
		}},
		{Label: "Set selected model as primary", Handler: func(m Model) (Model, tea.Cmd) {
			model, ok := m.selectedDetailModel()
			if !ok {
				m.notice = "no model selected"
				return m, nil
			}
			m = m.setProviderActiveModel(name, model)
			m = m.setPrimaryProvider(name)
			ref := name + ":" + model
			path, err := m.persistTierSelection("frontier", "primary", ref)
			if err != nil {
				m.notice = "frontier primary save failed: " + err.Error()
				return m, nil
			}
			if m.eng != nil {
				m.eng.SetProviderModel(name, model)
				m.status = m.eng.Status()
			}
			m.notice = fmt.Sprintf("frontier primary -> %s (%s)", ref, displayConfigPath(path))
			return m, nil
		}},
		{Label: "Toggle selected model as fallback", Handler: func(m Model) (Model, tea.Cmd) {
			model, ok := m.selectedDetailModel()
			if !ok {
				m.notice = "no model selected"
				return m, nil
			}
			return m.toggleProviderFallbackModel(name, model), nil
		}},
		{Label: "Add model from models.dev catalog", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.modelPickerActive = true
			m.providers.modelPickerManual = false
			m.providers.modelPickerIndex = 0
			m.providers.modelPickerDraft = ""
			m.providers.modelPickerItems = m.loadModelsDevForProvider(name)
			return m, nil
		}},
		{Label: "Add custom model id", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.modelPickerActive = true
			m.providers.modelPickerManual = true
			m.providers.modelPickerDraft = ""
			return m, nil
		}},
		{Label: "Edit provider name settings or API key", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.profileEditMode = true
			m.providers.profileEditField = 0
			m.providers.profileEditDraft = ""
			return m, nil
		}},
		{Label: "Edit API key only", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.profileEditMode = true
			m.providers.profileEditField = 2
			m.providers.profileEditDraft = ""
			return m, nil
		}},
		{Label: "Make primary for chat", Handler: func(m Model) (Model, tea.Cmd) {
			return m.setPrimaryProvider(name), nil
		}},
		{Label: "Test connection", Handler: func(m Model) (Model, tea.Cmd) {
			return m.startProviderProbe(name)
		}},
		{Label: "Delete selected model", Handler: func(m Model) (Model, tea.Cmd) {
			model, ok := m.selectedDetailModel()
			if !ok {
				m.notice = "no model selected"
				return m, nil
			}
			m.providers.confirmAction = "delete_model"
			m.providers.confirmTarget = model
			return m, nil
		}},
		{Label: "Back to provider list", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.viewMode = "list"
			m.providers.detailProvider = ""
			m.providers.modelEditIdx = 0
			return m, nil
		}},
	}
	return m.openActionMenu("Providers", "Provider - "+name, actions)
}

func (m Model) selectedDetailModel() (string, bool) {
	models := m.detailProviderVisibleModels()
	if len(models) == 0 {
		return "", false
	}
	idx := m.providers.modelEditIdx
	if idx < 0 || idx >= len(models) {
		idx = 0
	}
	return strings.TrimSpace(models[idx]), strings.TrimSpace(models[idx]) != ""
}
