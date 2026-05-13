package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleProviderCatalogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	if !m.providers.catalogLoaded {
		m = m.loadProviderCatalogItems()
	}
	total := len(m.providers.catalogItems)
	switch msg.String() {
	case "esc":
		m.providers.viewMode = providerViewList
	case "down":
		if m.providers.catalogScroll+1 < total {
			m.providers.catalogScroll++
		}
	case "up":
		if m.providers.catalogScroll > 0 {
			m.providers.catalogScroll--
		}
	case "pgdown":
		if m.providers.catalogScroll+12 < total {
			m.providers.catalogScroll += 12
		} else if total > 0 {
			m.providers.catalogScroll = total - 1
		}
	case "pgup":
		if m.providers.catalogScroll >= 12 {
			m.providers.catalogScroll -= 12
		} else {
			m.providers.catalogScroll = 0
		}
	case "home":
		m.providers.catalogScroll = 0
	case "end":
		if total > 0 {
			m.providers.catalogScroll = total - 1
		}
	case "enter", "right", "space":
		if total > 0 {
			item := m.providers.catalogItems[clampScroll(m.providers.catalogScroll, total)]
			m = m.startCatalogProviderForm(item)
		}
	}
	return m, nil
}

func (m Model) handleCatalogProviderFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.providers.catalogFormField == 2 && msg.Type == tea.KeySpace {
		return m.cycleCatalogCompatible(1), nil
	}
	switch msg.String() {
	case "esc":
		m = m.closeProviderTextEdit()
		m.providers.viewMode = providerViewCatalog
		m.providers.catalogFormKey = ""
		m.notice = "provider add cancelled"
	case "enter":
		if m.providers.catalogFormField < 4 {
			return m.openCatalogFormFieldEditor(), nil
		}
		name := strings.TrimSpace(m.providers.catalogFormName)
		if name == "" {
			m.notice = "name is required"
			m.providers.catalogFormField = 0
			return m, nil
		}
		if err := m.createProviderFromCatalog(); err != nil {
			m.notice = "create failed: " + err.Error()
			return m, nil
		}
		m.providers.viewMode = providerViewDetail
		m.providers.detailProvider = name
		m.providers.catalogFormKey = ""
		m = m.refreshProvidersRows()
		m = m.focusProviderRow(name)
		m.notice = fmt.Sprintf("created %s from %s; key encrypted in %s", name, m.providers.catalogRefID, displayConfigPath(mustUserConfigPath(m)))
	case "tab":
		m.providers.catalogFormField = (m.providers.catalogFormField + 1) % 5
	case "shift+tab":
		m.providers.catalogFormField--
		if m.providers.catalogFormField < 0 {
			m.providers.catalogFormField = 4
		}
	case "down":
		if m.providers.catalogFormField < 4 {
			m.providers.catalogFormField++
		}
	case "up":
		if m.providers.catalogFormField > 0 {
			m.providers.catalogFormField--
		}
	case "left":
		if m.providers.catalogFormField == 2 {
			m = m.cycleCatalogCompatible(-1)
		}
	case "right", "space":
		if m.providers.catalogFormField == 2 {
			m = m.cycleCatalogCompatible(1)
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

func (m Model) openCatalogFormFieldEditor() Model {
	switch m.providers.catalogFormField {
	case 0:
		return m.beginProviderTextEdit(providerViewCatalogForm, 0, "Provider name", m.providers.catalogFormName, false)
	case 1:
		return m.beginProviderTextEdit(providerViewCatalogForm, 1, "Endpoint", m.providers.catalogFormURL, false)
	case 2:
		m = m.cycleCatalogCompatible(1)
		m.notice = "compatible cycles through supported protocols; it is not a text field"
		return m
	case 3:
		return m.beginProviderTextEdit(providerViewCatalogForm, 3, "API key", m.providers.catalogFormKey, true)
	default:
		return m
	}
}

var providerCompatibleOptions = []string{"openai-compatible", "openai", "anthropic", "google", "gemini", ""}

func providerProtocolAllowed(protocol string) bool {
	protocol = strings.TrimSpace(protocol)
	for _, option := range providerCompatibleOptions {
		if strings.EqualFold(option, protocol) {
			return true
		}
	}
	return false
}

func (m Model) cycleCatalogCompatible(delta int) Model {
	current := strings.TrimSpace(m.providers.catalogFormCompat)
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
	m.providers.catalogFormCompat = providerCompatibleOptions[idx]
	return m
}

func (m Model) setCatalogFormField(field int, value string) Model {
	switch field {
	case 0:
		m.providers.catalogFormName = value
	case 1:
		m.providers.catalogFormURL = value
	case 2:
		m.notice = "compatible cycles through supported protocols; arbitrary text ignored"
	case 3:
		clean, changed := cleanProviderSecretInput(value)
		if changed {
			m.notice = "key input: line breaks/control chars ignored"
		}
		m.providers.catalogFormKey = clean
	}
	return m
}

func (m Model) handleProviderTiersKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	refs := m.providerModelRefs()
	totalSlots := len(providerTierNames) * 4
	switch msg.String() {
	case "esc":
		m.providers.viewMode = providerViewList
	case "down":
		if m.providers.tierCursor+1 < totalSlots {
			m.providers.tierCursor++
		}
	case "up":
		if m.providers.tierCursor > 0 {
			m.providers.tierCursor--
		}
	case "right":
		if len(refs) > 0 && m.providers.modelCursor+1 < len(refs) {
			m.providers.modelCursor++
		}
	case "left":
		if m.providers.modelCursor > 0 {
			m.providers.modelCursor--
		}
	case "pgdown":
		if len(refs) > 0 {
			m.providers.modelCursor += 12
			if m.providers.modelCursor >= len(refs) {
				m.providers.modelCursor = len(refs) - 1
			}
		}
	case "pgup":
		m.providers.modelCursor -= 12
		if m.providers.modelCursor < 0 {
			m.providers.modelCursor = 0
		}
	case "home":
		m.providers.tierCursor = 0
	case "end":
		if totalSlots > 0 {
			m.providers.tierCursor = totalSlots - 1
		}
	case "enter", "space":
		if len(refs) == 0 {
			m.notice = "add a keyed provider first"
			return m, nil
		}
		tier := providerTierNames[m.providers.tierCursor/4]
		slot := tierSlotName(m.providers.tierCursor % 4)
		ref := refs[clampScroll(m.providers.modelCursor, len(refs))]
		path, err := m.persistTierSelection(tier, slot, ref)
		if err != nil {
			m.notice = "tier save failed: " + err.Error()
			return m, nil
		}
		if m.eng != nil {
			m.status = m.eng.Status()
		}
		m.notice = fmt.Sprintf("%s %s -> %s (%s)", tier, slot, ref, displayConfigPath(path))
	}
	return m, nil
}

func (m Model) handleProviderSkillsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	refs := append([]string{"frontier", "medium", "turbo", "weak"}, m.providerModelRefs()...)
	skills := collectSkills(m.projectRoot())
	switch msg.String() {
	case "esc":
		m.providers.viewMode = providerViewList
	case "down":
		if m.providers.skillCursor+1 < len(skills) {
			m.providers.skillCursor++
		}
	case "up":
		if m.providers.skillCursor > 0 {
			m.providers.skillCursor--
		}
	case "right":
		if len(refs) > 0 && m.providers.skillModelCursor+1 < len(refs) {
			m.providers.skillModelCursor++
		}
	case "left":
		if m.providers.skillModelCursor > 0 {
			m.providers.skillModelCursor--
		}
	case "pgdown":
		if len(refs) > 0 {
			m.providers.skillModelCursor += 12
			if m.providers.skillModelCursor >= len(refs) {
				m.providers.skillModelCursor = len(refs) - 1
			}
		}
	case "pgup":
		m.providers.skillModelCursor -= 12
		if m.providers.skillModelCursor < 0 {
			m.providers.skillModelCursor = 0
		}
	case "enter", "space":
		if len(skills) == 0 || len(refs) == 0 {
			m.notice = "skills or keyed models are empty"
			return m, nil
		}
		skill := skills[clampScroll(m.providers.skillCursor, len(skills))].Name
		ref := refs[clampScroll(m.providers.skillModelCursor, len(refs))]
		path, err := m.persistSkillModelSelection(skill, ref)
		if err != nil {
			m.notice = "skill route save failed: " + err.Error()
			return m, nil
		}
		m.notice = fmt.Sprintf("skill %s -> %s (%s)", skill, ref, displayConfigPath(path))
	}
	return m, nil
}

func tierSlotName(idx int) string {
	switch idx {
	case 0:
		return "primary"
	case 1:
		return "fallback1"
	case 2:
		return "fallback2"
	default:
		return "fallback3"
	}
}

func mustUserConfigPath(m Model) string {
	path, err := m.userConfigPath()
	if err != nil {
		return "~/.dfmc/config.yaml"
	}
	return path
}
