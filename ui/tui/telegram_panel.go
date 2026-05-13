package tui

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/bot"
	"github.com/dontfuckmycode/dfmc/internal/config"
)

var telegramPanelStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.RoundedBorder()).
	BorderForeground(colorTabTelegram).
	Padding(1, 2)

func (m *Model) renderTelegramPanel() string {
	width := m.width
	if width < 40 {
		width = 40
	}
	height := m.height
	if height < 15 {
		height = 15
	}

	b := &strings.Builder{}
	b.WriteString(telegramPanelStyle.Render("Telegram Bot") + "\n")

	tokenSet := false
	if m.eng != nil && m.eng.Config != nil {
		tokenSet = m.eng.Config.Telegram.Enabled && m.eng.Config.Telegram.Token != ""
	}

	if m.telegram.formActive {
		b.WriteString(m.renderTelegramForm(width))
		return b.String()
	}

	if !tokenSet {
		b.WriteString(m.renderTelegramSetupPrompt(width))
		return b.String()
	}

	b.WriteString(okStyle.Render("● Connected") + "  ")
	b.WriteString(subtleStyle.Render(fmt.Sprintf("Users: %d | Registered: %d",
		len(m.eng.Config.Telegram.AllowedUsers),
		m.telegram.registeredCount,
	)))
	b.WriteString("\n\n")

	if len(m.telegram.messages) == 0 {
		b.WriteString(subtleStyle.Render("  No messages yet.\n"))
		b.WriteString(subtleStyle.Render("  Send a message from Telegram or use /chat\n"))
	} else {
		lines := m.telegram.messages
		if m.telegram.scroll > 0 && m.telegram.scroll < len(lines) {
			lines = lines[m.telegram.scroll:]
		}
		show := lines
		if len(show) > height-12 {
			show = show[len(show)-(height-12):]
		}
		for _, msg := range show {
			prefix := "📥"
			if msg.isOut {
				prefix = "📤"
			}
			text := msg.text
			if len(text) > width-10 {
				text = text[:width-13] + "…"
			}
			b.WriteString(fmt.Sprintf("  %s %s %s\n", prefix, boldStyle.Render(msg.from), subtleStyle.Render(msg.time)))
			b.WriteString(fmt.Sprintf("    %s\n", text))
		}
	}

	b.WriteString("\n" + subtleStyle.Render("↑↓ scroll · enter/a actions · esc close"))
	return b.String()
}

func (m *Model) renderTelegramSetupPrompt(width int) string {
	b := &strings.Builder{}
	b.WriteString(warnStyle.Render("⚠ Telegram not configured") + "\n\n")

	selectedField := 0
	if m.telegram.setupSelected == 1 {
		selectedField = 1
	}

	tokenPrefix := "  "
	if selectedField == 0 {
		tokenPrefix = okStyle.Render(" ▶ ")
	}
	b.WriteString(fmt.Sprintf("%s%s %s\n", tokenPrefix, boldStyle.Render("Token:"), subtleStyle.Render("(not set — click to set)")))

	usersPrefix := "  "
	if selectedField == 1 {
		usersPrefix = okStyle.Render(" ▶ ")
	}
	b.WriteString(fmt.Sprintf("%s%s %s\n\n", usersPrefix, boldStyle.Render("Allowed Users:"), subtleStyle.Render("(none — click to add)")))

	b.WriteString(subtleStyle.Render("  ↑↓ navigate · Enter edit · Esc close"))
	return b.String()
}

func (m Model) openTelegramActionMenu() Model {
	actions := []panelAction{
		{Label: "Edit Config", Accel: "e", Handler: func(m Model) (Model, tea.Cmd) {
			return m.openTelegramForm(), nil
		}},
		{Label: "Test Message…", Accel: "t", Handler: func(m Model) (Model, tea.Cmd) {
			c := m
			c.telegram.testMsgActive = true
			c.telegram.testMsgInput = ""
			return c, nil
		}},
		{Label: "Disconnect bot", Accel: "d", Handler: func(m Model) (Model, tea.Cmd) {
			c := m
			if c.eng != nil && c.eng.Config != nil {
				c.eng.Config.Telegram.Enabled = false
				c.eng.Config.Telegram.Token = ""
				_ = c.eng.Config.Save(filepath.Join(config.UserConfigDir(), "config.yaml"))
			}
			c.telegram.messages = nil
			c.telegram.botUsername = ""
			c.telegram.registeredCount = 0
			c.telegram.inputActive = false
			c.telegram.formActive = false
			c.notice = "Telegram bot disconnected"
			return c, nil
		}},
	}
	return m.openActionMenu("Telegram", "Telegram actions", actions)
}

type telegramPanelState struct {
	messages          []telegramMessageItem
	scroll            int
	setupSelected     int
	inputActive       bool
	formActive        bool
	editingToken      bool
	editingUsers      bool
	tokenInput        string
	allowedUsersInput string
	saveSuccess       bool
	saveError         string
	testMsgActive     bool
	testMsgInput      string
	botUsername       string
	registeredCount   int
	telegramBot       *bot.TelegramBot
}

type telegramMessageItem struct {
	from  string
	text  string
	time  string
	isOut bool
}

func (m *Model) InitBotPanel() {
	m.telegram = telegramPanelState{
		messages:          []telegramMessageItem{},
		scroll:            0,
		setupSelected:     0,
		inputActive:       false,
		formActive:        false,
		editingToken:      true,
		editingUsers:      false,
		tokenInput:        "",
		allowedUsersInput: "",
		saveSuccess:       false,
		saveError:         "",
		testMsgActive:     false,
		testMsgInput:      "",
		telegramBot:       nil,
	}
}

func (m Model) openTelegramForm() Model {
	m.telegram.formActive = true
	m.telegram.inputActive = true
	m.telegram.editingToken = true
	m.telegram.editingUsers = false
	m.telegram.tokenInput = ""
	m.telegram.allowedUsersInput = ""
	m.telegram.saveSuccess = false
	m.telegram.saveError = ""
	if m.eng != nil && m.eng.Config != nil {
		m.telegram.tokenInput = m.eng.Config.Telegram.Token
		if len(m.eng.Config.Telegram.AllowedUsers) > 0 {
			parts := make([]string, len(m.eng.Config.Telegram.AllowedUsers))
			for i, id := range m.eng.Config.Telegram.AllowedUsers {
				parts[i] = strconv.FormatInt(id, 10)
			}
			m.telegram.allowedUsersInput = strings.Join(parts, ", ")
		}
	}
	return m
}

func (m Model) closeTelegramForm() Model {
	m.telegram.formActive = false
	m.telegram.inputActive = false
	m.telegram.editingToken = false
	m.telegram.editingUsers = false
	m.telegram.saveSuccess = false
	m.telegram.saveError = ""
	m.telegram.testMsgActive = false
	m.telegram.testMsgInput = ""
	return m
}

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

	if msg.Type == tea.KeyUp && m.telegram.scroll > 0 {
		m.telegram.scroll--
	}
	if msg.Type == tea.KeyDown {
		maxScroll := len(m.telegram.messages) - 1
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.telegram.scroll < maxScroll {
			m.telegram.scroll++
		}
	}
	if msg.String() == "pgup" {
		m.telegram.scroll -= 10
		if m.telegram.scroll < 0 {
			m.telegram.scroll = 0
		}
	}
	if msg.String() == "pgdown" {
		maxScroll := len(m.telegram.messages) - 1
		if maxScroll < 0 {
			maxScroll = 0
		}
		m.telegram.scroll += 10
		if m.telegram.scroll > maxScroll {
			m.telegram.scroll = maxScroll
		}
	}
	if msg.String() == "g" {
		m.telegram.scroll = 0
	}
	if msg.String() == "G" {
		m.telegram.scroll = len(m.telegram.messages) - 1
		if m.telegram.scroll < 0 {
			m.telegram.scroll = 0
		}
	}
	return m, nil
}

func (m *Model) handleTelegramFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.telegram.formActive {
		return m, nil
	}
	if msg.Type == tea.KeyTab {
		if m.telegram.editingToken {
			m.telegram.editingToken = false
			m.telegram.editingUsers = true
		} else {
			m.telegram.editingToken = true
			m.telegram.editingUsers = false
		}
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
	if msg.Type == tea.KeyUp || msg.Type == tea.KeyDown {
		if m.telegram.editingToken {
			m.telegram.editingToken = false
			m.telegram.editingUsers = true
		} else {
			m.telegram.editingToken = true
			m.telegram.editingUsers = false
		}
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
			} else if m.telegram.editingUsers {
				if (r >= '0' && r <= '9') || r == ',' || r == ' ' || r == '-' {
					m.telegram.allowedUsersInput += string(r)
				}
			}
		}
	}
	return m, nil
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

func (m *Model) sendTelegramTestMessage() (tea.Model, tea.Cmd) {
	msg := strings.TrimSpace(m.telegram.testMsgInput)
	if msg == "" {
		m.telegram.testMsgActive = false
		m.telegram.testMsgInput = ""
		return m, nil
	}

	var sendErr string
	if m.telegram.telegramBot != nil && len(m.telegram.messages) > 0 {
		var targetUserID int64
		for i := len(m.telegram.messages) - 1; i >= 0; i-- {
			if !m.telegram.messages[i].isOut {
				if strings.HasPrefix(m.telegram.messages[i].from, "User ") {
					if id, err := strconv.ParseInt(strings.TrimPrefix(m.telegram.messages[i].from, "User "), 10, 64); err == nil {
						targetUserID = id
						break
					}
				}
				break
			}
		}
		if targetUserID > 0 {
			sendErr = m.telegram.telegramBot.SendToUser(targetUserID, msg).Error()
		} else {
			sendErr = "(no registered user to send to)"
		}
	} else {
		sendErr = "(bot not connected)"
	}

	displayText := msg
	if sendErr != "" {
		displayText = msg + " ⚠ " + sendErr
	}
	outMsg := telegramMessageItem{from: "DFMC", text: displayText, time: time.Now().Format("15:04"), isOut: true}
	if len(m.telegram.messages) >= 100 {
		m.telegram.messages = m.telegram.messages[1:]
	}
	m.telegram.messages = append(m.telegram.messages, outMsg)
	m.telegram.scroll = len(m.telegram.messages) - 1
	m.telegram.testMsgActive = false
	m.telegram.testMsgInput = ""
	return m, nil
}

func (m *Model) renderTelegramForm(width int) string {
	b := &strings.Builder{}
	b.WriteString(titleStyle.Bold(true).Render("  Telegram Configuration\n"))
	b.WriteString(renderDivider(width-2) + "\n")

	tokenLabel := "Bot Token"
	if m.telegram.editingToken {
		tokenLabel += " ◀"
	}
	disp := m.telegram.tokenInput
	if disp == "" {
		disp = subtleStyle.Render("(paste your BotFather token here)")
	}
	b.WriteString(fmt.Sprintf("  %s %s\n", boldStyle.Render(tokenLabel+":"), disp))
	b.WriteString(subtleStyle.Render("  Get token from @BotFather on Telegram\n\n"))

	usersLabel := "Allowed User IDs"
	if m.telegram.editingUsers {
		usersLabel += " ◀"
	}
	usersDisp := m.telegram.allowedUsersInput
	if usersDisp == "" {
		usersDisp = "(optional — comma-separated numeric IDs)"
	} else {
		usersDisp = truncateSingleLine(usersDisp, width-35)
	}
	b.WriteString(fmt.Sprintf("  %s %s\n", boldStyle.Render(usersLabel+":"), usersDisp))
	b.WriteString(subtleStyle.Render("  Get your ID from @userinfobot on Telegram\n\n"))

	if m.telegram.saveSuccess {
		b.WriteString(okStyle.Render("  ✓ Saved! Bot connecting…\n\n"))
	} else if m.telegram.saveError != "" {
		b.WriteString(warnStyle.Render("  ✗ " + m.telegram.saveError + "\n\n"))
	}
	b.WriteString(subtleStyle.Render("  Tab: switch field · Enter: save · Esc: cancel\n"))
	b.WriteString(subtleStyle.Render("  Type or paste normally — no Alt keys needed.\n"))
	return b.String()
}

func (m *Model) saveTelegramSetup() *Model {
	m.telegram.saveError = ""
	m.telegram.saveSuccess = false

	if m.eng == nil || m.eng.Config == nil {
		m.telegram.saveError = "Engine not ready"
		return m
	}
	token := strings.TrimSpace(m.telegram.tokenInput)
	if token == "" {
		m.telegram.saveError = "Token cannot be empty"
		return m
	}

	var allowedIDs []int64
	usersRaw := strings.TrimSpace(m.telegram.allowedUsersInput)
	if usersRaw != "" {
		for _, p := range strings.Split(usersRaw, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			id, err := strconv.ParseInt(p, 10, 64)
			if err != nil || id <= 0 {
				m.telegram.saveError = fmt.Sprintf("Invalid user ID: %s (must be numeric)", p)
				return m
			}
			allowedIDs = append(allowedIDs, id)
		}
	}

	m.eng.Config.Telegram.Token = token
	m.eng.Config.Telegram.Enabled = true
	m.eng.Config.Telegram.AllowedUsers = allowedIDs

	if err := m.eng.Config.Save(filepath.Join(config.UserConfigDir(), "config.yaml")); err != nil {
		m.telegram.saveError = fmt.Sprintf("Save failed: %v", err)
		return m
	}

	tgBot, err := bot.New(token)
	if err != nil {
		m.telegram.saveError = fmt.Sprintf("Invalid token: %v", err)
		return m
	}
	tgBot.SetAllowedUsers(allowedIDs)
	tgBot.SetOnMessage(func(userID int64, text string, replyFn func(string)) {
		_ = m.addTelegramMessage(telegramMessageItem{from: fmt.Sprintf("User %d", userID), text: text, time: time.Now().Format("15:04"), isOut: false})
		go func() {
			resp, err := m.eng.Ask(context.Background(), text)
			if err != nil {
				replyFn("⚠️ Error: " + err.Error())
				return
			}
			if len(resp) > 4000 {
				resp = resp[:3997] + "..."
			}
			_ = m.addTelegramMessage(telegramMessageItem{from: "DFMC", text: resp, time: time.Now().Format("15:04"), isOut: true})
			replyFn(resp)
		}()
	})

	go func() {
		if err := tgBot.Start(); err != nil {
			log.Printf("[telegram] bot start error: %v", err)
		}
	}()

	if health := tgBot.Health(); health != nil {
		if username, ok := health["bot_username"].(string); ok {
			m.telegram.botUsername = username
		}
	}
	m.telegram.telegramBot = tgBot
	m.telegram.saveSuccess = true
	m.telegram.formActive = false
	m.telegram.inputActive = false
	return m
}

func (m *Model) handleTelegramMessageAdded(msg telegramMessageAddedMsg) (tea.Model, tea.Cmd) {
	if len(m.telegram.messages) >= 100 {
		m.telegram.messages = m.telegram.messages[1:]
	}
	m.telegram.messages = append(m.telegram.messages, msg.msg)
	m.telegram.scroll = len(m.telegram.messages) - 1
	if m.telegram.scroll < 0 {
		m.telegram.scroll = 0
	}
	return m, nil
}

func (m *Model) addTelegramMessage(msg telegramMessageItem) tea.Cmd {
	return func() tea.Msg {
		return telegramMessageAddedMsg{msg: msg}
	}
}

type telegramMessageAddedMsg struct {
	msg telegramMessageItem
}
