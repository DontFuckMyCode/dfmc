package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleProviderInputTextKey(msg tea.KeyMsg) (Model, bool) {
	if m.providers.viewMode == providerViewCatalogForm && m.providers.catalogFormField == 2 && msg.Type == tea.KeySpace {
		return m, false
	}
	if m.providers.viewMode == providerViewCatalogForm && textKeyMsg(msg) {
		m.notice = "press Enter on a field before typing or pasting"
		return m, true
	}
	if !m.providers.textEditActive {
		return m, false
	}
	if text, ok := providerInputText(msg); ok {
		return m.appendProviderTextEdit(text), true
	}
	return m, false
}

func providerInputText(msg tea.KeyMsg) (string, bool) {
	switch msg.Type {
	case tea.KeyRunes:
		return string(msg.Runes), len(msg.Runes) > 0
	case tea.KeySpace:
		return " ", true
	}
	if msg.Paste {
		switch msg.Type {
		case tea.KeyEnter:
			return "\n", true
		case tea.KeyTab:
			return "\t", true
		}
	}
	return "", false
}

func cleanProviderSecretInput(text string) (string, bool) {
	var b strings.Builder
	changed := false
	for _, r := range text {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 || r == 0x7f {
			changed = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), changed
}

func cleanProviderFieldInput(text string) (string, bool) {
	clean := providerDisplayLine(text)
	return clean, clean != strings.TrimSpace(text)
}

func providerDisplayLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	var b strings.Builder
	changed := false
	for _, r := range text {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 || r == 0x7f {
			changed = true
			if b.Len() > 0 {
				b.WriteRune(' ')
			}
			continue
		}
		b.WriteRune(r)
	}
	if !changed {
		return text
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func providerOpenAIRequestURL(protocol, baseURL string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol != "openai" && protocol != "openai-compatible" {
		return ""
	}
	baseURL = providerDisplayLine(baseURL)
	if baseURL == "" {
		return ""
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(strings.ToLower(baseURL), "/chat/completions") {
		return baseURL
	}
	return baseURL + "/chat/completions"
}

func textKeyMsg(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace || msg.Paste
}

func (m Model) beginProviderTextEdit(target string, field int, title, value string, secret bool) Model {
	m.providers.textEditActive = true
	m.providers.textEditTarget = target
	m.providers.textEditField = field
	m.providers.textEditTitle = title
	m.providers.textEditDraft = value
	m.providers.textEditSecret = secret
	m.notice = "input box: paste text, Enter saves, Esc cancels"
	return m
}

func (m Model) handleProviderTextEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m = m.commitProviderTextEdit()
		return m, nil
	case tea.KeyEsc:
		m = m.closeProviderTextEdit()
		m.notice = "input cancelled"
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.providers.textEditDraft); len(r) > 0 {
			m.providers.textEditDraft = string(r[:len(r)-1])
		}
		return m, nil
	}
	switch msg.String() {
	case "ctrl+u":
		m.providers.textEditDraft = ""
		return m, nil
	}
	if text, ok := providerInputText(msg); ok {
		m = m.appendProviderTextEdit(text)
		return m, nil
	}
	return m, nil
}

func (m Model) appendProviderTextEdit(text string) Model {
	if m.providers.textEditSecret {
		clean, changed := cleanProviderSecretInput(text)
		if changed {
			m.notice = "key input: line breaks/control chars ignored"
		}
		m.providers.textEditDraft += clean
		return m
	}
	clean, changed := cleanProviderFieldInput(text)
	if changed {
		m.notice = "input: line breaks/control chars converted to spaces"
	}
	if m.providers.textEditDraft == "" {
		m.providers.textEditDraft = clean
	} else {
		m.providers.textEditDraft += clean
	}
	return m
}

func (m Model) commitProviderTextEdit() Model {
	target := m.providers.textEditTarget
	field := m.providers.textEditField
	draft := m.providers.textEditDraft
	secret := m.providers.textEditSecret
	m = m.closeProviderTextEdit()
	if secret {
		clean, changed := cleanProviderSecretInput(strings.TrimSpace(draft))
		if changed {
			m.notice = "key input: line breaks/control chars ignored"
		}
		draft = clean
	} else {
		clean, changed := cleanProviderFieldInput(draft)
		if changed {
			m.notice = "input: line breaks/control chars converted to spaces"
		}
		draft = clean
	}
	switch target {
	case providerViewCatalogForm:
		m = m.setCatalogFormField(field, draft)
	case "new_provider":
		m.providers.newProviderDraft = draft
	case "profile_edit":
		m.providers.profileEditField = field
		m.providers.profileEditDraft = draft
		m.commitProfileEditField()
		m.providers.profileEditDraft = ""
		if err := m.persistProfileEdits(); err != nil {
			m.notice = "save failed: " + err.Error()
		} else {
			m = m.refreshProvidersRows()
			m = m.focusProviderRow(m.providers.detailProvider)
			m.notice = "saved profile for " + m.providers.detailProvider
		}
	}
	return m
}

func (m Model) closeProviderTextEdit() Model {
	m.providers.textEditActive = false
	m.providers.textEditTarget = ""
	m.providers.textEditField = 0
	m.providers.textEditTitle = ""
	m.providers.textEditDraft = ""
	m.providers.textEditSecret = false
	return m
}
