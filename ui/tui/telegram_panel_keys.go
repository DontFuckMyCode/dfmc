package tui

import tea "github.com/charmbracelet/bubbletea"

func (m *Model) handleTelegramOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	if m.telegram.formActive {
		return m.handleTelegramFormKey(msg)
	}
	if m.telegram.testMsgActive {
		return m.handleTelegramTestMsgKey(msg)
	}
	if msg.String() == "q" || msg.Type == tea.KeyEsc {
		m.ui.panelOverlayKind = ""
		m.notice = "Telegram panel closed."
		return m, nil
	}

	tokenSet := false
	if m.eng != nil && m.eng.Config != nil {
		tokenSet = m.eng.Config.Telegram.Enabled && m.eng.Config.Telegram.Token != ""
	}
	if !tokenSet && !m.telegram.formActive {
		if msg.Type == tea.KeyUp && m.telegram.setupSelected > 0 {
			m.telegram.setupSelected--
		}
		if msg.Type == tea.KeyDown && m.telegram.setupSelected < 1 {
			m.telegram.setupSelected++
		}
		if msg.Type == tea.KeyEnter || msg.String() == "o" {
			next := m.openTelegramForm()
			*m = next
			m.telegram.editingToken = m.telegram.setupSelected == 0
			m.telegram.editingUsers = m.telegram.setupSelected == 1
			return m, nil
		}
		return m, nil
	}
	if msg.Type == tea.KeyEnter || msg.String() == "a" {
		next := m.openTelegramActionMenu()
		return next, nil
	}

	m.handleTelegramScrollKey(msg)
	return m, nil
}

func (m *Model) handleTelegramScrollKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "up":
		if m.telegram.scroll > 0 {
			m.telegram.scroll--
		}
	case "down":
		if m.telegram.scroll < telegramMaxScroll(m.telegram.messages) {
			m.telegram.scroll++
		}
	case "pgup":
		m.telegram.scroll -= 10
		if m.telegram.scroll < 0 {
			m.telegram.scroll = 0
		}
	case "pgdown":
		m.telegram.scroll += 10
		if maxScroll := telegramMaxScroll(m.telegram.messages); m.telegram.scroll > maxScroll {
			m.telegram.scroll = maxScroll
		}
	case "g":
		m.telegram.scroll = 0
	case "G":
		m.telegram.scroll = telegramMaxScroll(m.telegram.messages)
	}
}

func telegramMaxScroll(messages []telegramMessageItem) int {
	maxScroll := len(messages) - 1
	if maxScroll < 0 {
		return 0
	}
	return maxScroll
}

func (m *Model) handleTelegramFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.telegram.formActive {
		return m, nil
	}
	if msg.Type == tea.KeyEnter {
		m = m.saveTelegramSetup()
		return m, nil
	}
	if msg.Type == tea.KeyEsc {
		m.closeTelegramForm()
		return m, nil
	}
	if msg.Type == tea.KeyBackspace {
		if m.telegram.editingToken && len(m.telegram.tokenInput) > 0 {
			m.telegram.tokenInput = m.telegram.tokenInput[:len(m.telegram.tokenInput)-1]
		} else if m.telegram.editingUsers && len(m.telegram.allowedUsersInput) > 0 {
			m.telegram.allowedUsersInput = m.telegram.allowedUsersInput[:len(m.telegram.allowedUsersInput)-1]
		}
		return m, nil
	}
	if msg.Type == tea.KeyDelete {
		if m.telegram.editingToken {
			m.telegram.tokenInput = ""
		} else if m.telegram.editingUsers {
			m.telegram.allowedUsersInput = ""
		}
		return m, nil
	}
	if msg.Type == tea.KeySpace {
		if m.telegram.editingToken {
			m.telegram.tokenInput += " "
		} else if m.telegram.editingUsers {
			m.telegram.allowedUsersInput += " "
		}
		return m, nil
	}
	if len(msg.Runes) > 0 {
		r := msg.Runes[0]
		if r >= 32 && r <= 126 {
			if m.telegram.editingToken {
				m.telegram.tokenInput += string(r)
			} else if m.telegram.editingUsers && telegramAllowedUsersRune(r) {
				m.telegram.allowedUsersInput += string(r)
			}
		}
	}
	return m, nil
}

func telegramAllowedUsersRune(r rune) bool {
	return (r >= '0' && r <= '9') || r == ',' || r == ' ' || r == '-'
}

func (m *Model) handleTelegramTestMsgKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.telegram.testMsgActive {
		return m, nil
	}
	if msg.Type == tea.KeyEnter {
		return m.sendTelegramTestMessage()
	}
	if msg.Type == tea.KeyEsc {
		m.telegram.testMsgActive = false
		m.telegram.testMsgInput = ""
		return m, nil
	}
	if msg.Type == tea.KeyBackspace && len(m.telegram.testMsgInput) > 0 {
		m.telegram.testMsgInput = m.telegram.testMsgInput[:len(m.telegram.testMsgInput)-1]
	}
	if msg.Type == tea.KeyDelete {
		m.telegram.testMsgInput = ""
	}
	if msg.Type == tea.KeySpace {
		m.telegram.testMsgInput += " "
	}
	if len(msg.Runes) > 0 {
		r := msg.Runes[0]
		if r >= 32 && r <= 126 {
			m.telegram.testMsgInput += string(r)
		}
	}
	return m, nil
}
