package tui

import "strings"

func (m Model) activateDiagnosticTab(label string) Model {
	if idx := m.activityTabIndex(label); idx >= 0 {
		m.activeTab = idx
	}
	return m
}

func (m Model) activatePlansPanel(query string, refresh bool) Model {
	m = m.activateDiagnosticTab("Plans")
	previousQuery := strings.TrimSpace(m.plans.query)
	if seeded := strings.TrimSpace(query); seeded != "" {
		m.plans.query = seeded
	}
	currentQuery := strings.TrimSpace(m.plans.query)
	if refresh || (currentQuery != "" && (m.plans.plan == nil || !strings.EqualFold(previousQuery, currentQuery))) {
		m = m.runPlansSplit()
	}
	return m
}

func (m Model) activateContextPanel(query string, refresh bool) Model {
	m = m.activateDiagnosticTab("Context")
	previousQuery := strings.TrimSpace(m.contextPanel.query)
	if seeded := strings.TrimSpace(query); seeded != "" {
		m.contextPanel.query = seeded
	}
	currentQuery := strings.TrimSpace(m.contextPanel.query)
	if refresh || (currentQuery != "" && (m.contextPanel.preview == nil || !strings.EqualFold(previousQuery, currentQuery))) {
		m = m.runContextPreview()
	}
	return m
}

func (m Model) activateProvidersPanel(provider string, refresh bool) Model {
	m = m.activateDiagnosticTab("Providers")
	if refresh || len(m.providers.rows) == 0 {
		m = m.refreshProvidersRows()
	}
	if focused := strings.TrimSpace(provider); focused != "" {
		m = m.focusProviderRow(focused)
	}
	return m
}
