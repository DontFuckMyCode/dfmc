package tui

import (
	"context"
	"fmt"
	"os"
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
	return m.renderTelegramPanelSized(m.width)
}

func (m *Model) renderTelegramPanelSized(contentWidth int) string {
	width := max(contentWidth, 40)
	height := max(m.height, 15)

	b := &strings.Builder{}
	b.WriteString(telegramPanelStyle.Render("Telegram Bot"))
	b.WriteByte('\n')

	tokenSet := false
	if m.eng != nil && m.eng.Config != nil {
		tokenSet = m.eng.Config.Telegram.Enabled && m.eng.Config.Telegram.Token != ""
	}

	if m.telegram.formActive {
		b.WriteString(m.renderTelegramForm(width))
		return b.String()
	}

	if !tokenSet {
		b.WriteString(m.renderTelegramSetupPrompt())
		return b.String()
	}

	b.WriteString(okStyle.Render("● Connected"))
	b.WriteString("  ")
	registered := m.telegramRegisteredCount()
	username := blankFallback(m.telegram.botUsername, "bot")
	b.WriteString(subtleStyle.Render(fmt.Sprintf("@%s · Users: %d · Registered: %d",
		username,
		len(m.eng.Config.Telegram.AllowedUsers),
		registered,
	)))
	b.WriteString("\n\n")

	if len(m.telegram.messages) == 0 {
		b.WriteString(subtleStyle.Render("  No messages yet.\n"))
		b.WriteString(subtleStyle.Render("  Send a message from Telegram or use /chat\n"))
	} else {
		b.WriteString(m.renderTelegramMessageHistory(width, height-12))
	}

	b.WriteByte('\n')
	b.WriteString(subtleStyle.Render("↑↓ scroll · enter action menu · esc close"))
	return b.String()
}

func (m *Model) renderTelegramSetupPrompt() string {
	b := &strings.Builder{}
	b.WriteString(warnStyle.Render("⚠ Telegram not configured"))
	b.WriteString("\n\n")

	selectedField := 0
	if m.telegram.setupSelected == 1 {
		selectedField = 1
	}

	tokenPrefix := "  "
	if selectedField == 0 {
		tokenPrefix = okStyle.Render(" ▶ ")
	}
	fmt.Fprintf(b, "%s%s %s\n", tokenPrefix, boldStyle.Render("Token:"), subtleStyle.Render("(not set — click to set)"))

	usersPrefix := "  "
	if selectedField == 1 {
		usersPrefix = okStyle.Render(" ▶ ")
	}
	fmt.Fprintf(b, "%s%s %s\n\n", usersPrefix, boldStyle.Render("Allowed Users:"), subtleStyle.Render("(none — click to add)"))

	b.WriteString(subtleStyle.Render("  ↑↓ navigate · Enter edit · Esc close"))
	return b.String()
}

func (m Model) openTelegramActionMenu() Model {
	actions := []panelAction{
		{Label: "Edit Config", Accel: "e", Handler: func(m Model) (Model, tea.Cmd) {
			return m.openTelegramForm(), nil
		}},
		{Label: "Edit allowed users", Accel: "u", Handler: func(m Model) (Model, tea.Cmd) {
			return m.openTelegramUsersForm(), nil
		}},
		{Label: "Test Message…", Accel: "t", Handler: func(m Model) (Model, tea.Cmd) {
			c := m
			c.telegram.testMsgActive = true
			c.telegram.testMsgInput = ""
			return c, nil
		}},
		{Label: "Clear panel history", Accel: "c", Handler: func(m Model) (Model, tea.Cmd) {
			c := m
			c.telegram.messages = nil
			c.telegram.scroll = 0
			c.notice = "Telegram panel history cleared"
			return c, nil
		}},
		{Label: "Disconnect bot", Accel: "d", Handler: func(m Model) (Model, tea.Cmd) {
			c := m
			c.disconnectTelegramBot()
			return c, nil
		}},
	}
	return m.openActionMenu("Telegram", "Telegram actions", actions)
}

type telegramPanelState struct {
	messages          []telegramMessageItem
	events            chan telegramMessageItem
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
		events:            make(chan telegramMessageItem, 100),
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
	return m.openTelegramFormField(false)
}

func (m Model) openTelegramUsersForm() Model {
	return m.openTelegramFormField(true)
}

func (m Model) openTelegramFormField(editUsers bool) Model {
	m.telegram.formActive = true
	m.telegram.inputActive = true
	m.telegram.editingToken = !editUsers
	m.telegram.editingUsers = editUsers
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

func (m *Model) renderTelegramMessageHistory(width, maxRows int) string {
	if maxRows <= 0 {
		maxRows = 6
	}
	innerWidth := max(width-8, 24)
	items := m.telegram.messages
	if m.telegram.scroll > 0 && m.telegram.scroll < len(items) {
		items = items[m.telegram.scroll:]
	}
	rows := make([]string, 0, maxRows)
	for _, msg := range items {
		rows = append(rows, renderTelegramMessageRows(msg, innerWidth)...)
		if len(rows) > maxRows {
			rows = rows[len(rows)-maxRows:]
		}
	}
	return strings.Join(rows, "\n") + "\n"
}

func renderTelegramMessageRows(msg telegramMessageItem, width int) []string {
	width = max(width, 24)
	prefix := "IN "
	if msg.isOut {
		prefix = "OUT"
	}
	header := fmt.Sprintf("  %s  %s  %s", prefix, truncateSingleLine(msg.from, 24), msg.time)
	rows := []string{truncateSingleLine(header, width)}
	bodyWidth := max(width-4, 16)
	text := strings.TrimSpace(msg.text)
	if text == "" {
		text = "(empty)"
	}
	for part := range strings.SplitSeq(text, "\n") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for _, row := range wrapBubbleLine(part, bodyWidth) {
			rows = append(rows, "    "+truncateSingleLine(row, bodyWidth))
		}
	}
	if len(rows) == 1 {
		rows = append(rows, "    "+subtleStyle.Render("(empty)"))
	}
	return rows
}

func (m *Model) appendTelegramLog(from, text string, isOut bool) {
	m.addTelegramMessageDirect(telegramMessageItem{
		from:  strings.TrimSpace(from),
		text:  strings.TrimSpace(text),
		time:  time.Now().Format("15:04"),
		isOut: isOut,
	})
}

func (m *Model) addTelegramMessageDirect(msg telegramMessageItem) {
	if strings.TrimSpace(msg.from) == "" {
		msg.from = "Telegram"
	}
	if strings.TrimSpace(msg.time) == "" {
		msg.time = time.Now().Format("15:04")
	}
	if len(m.telegram.messages) >= 100 {
		m.telegram.messages = m.telegram.messages[1:]
	}
	m.telegram.messages = append(m.telegram.messages, msg)
	m.telegram.scroll = max(len(m.telegram.messages)-1, 0)
}

func (m *Model) bindTelegramBotToPanel(tgBot *bot.TelegramBot) {
	if m == nil || tgBot == nil {
		return
	}
	m.ensureDiagnostics()
	events := m.telegram.events
	eng := m.eng
	tgBot.SetLogger(func(format string, args ...any) {
		enqueueTelegramPanelMessage(events, telegramMessageItem{
			from:  "Log",
			text:  formatTelegramLog(format, args...),
			time:  time.Now().Format("15:04"),
			isOut: false,
		})
	})
	tgBot.SetOnMessage(func(userID int64, text string, replyFn func(string)) {
		enqueueTelegramPanelMessage(events, telegramMessageItem{from: fmt.Sprintf("User %d", userID), text: text, time: time.Now().Format("15:04"), isOut: false})
		go func() {
			if eng == nil {
				replyFn("DFMC engine not connected.")
				return
			}
			resp, err := eng.Ask(context.Background(), text)
			if err != nil {
				errText := "Error: " + err.Error()
				enqueueTelegramPanelMessage(events, telegramMessageItem{from: "DFMC", text: errText, time: time.Now().Format("15:04"), isOut: true})
				replyFn("⚠️ " + errText)
				return
			}
			resp = truncateRunes(resp, 4000, "...")
			enqueueTelegramPanelMessage(events, telegramMessageItem{from: "DFMC", text: resp, time: time.Now().Format("15:04"), isOut: true})
			replyFn(resp)
		}()
	})
}

func enqueueTelegramPanelMessage(ch chan telegramMessageItem, msg telegramMessageItem) {
	if ch == nil {
		return
	}
	if msg.time == "" {
		msg.time = time.Now().Format("15:04")
	}
	select {
	case ch <- msg:
	default:
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- msg:
		default:
		}
	}
}

func formatTelegramLog(format string, args ...any) string {
	text := strings.TrimSpace(fmt.Sprintf(format, args...))
	text = strings.TrimSpace(strings.TrimPrefix(text, "[telegram]"))
	if text == "" {
		return "telegram event"
	}
	return text
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
				if rest, ok := strings.CutPrefix(m.telegram.messages[i].from, "User "); ok {
					if id, err := strconv.ParseInt(rest, 10, 64); err == nil {
						targetUserID = id
						break
					}
				}
				break
			}
		}
		if targetUserID > 0 {
			if err := m.telegram.telegramBot.SendToUser(targetUserID, msg); err != nil {
				sendErr = err.Error()
			}
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
	m.addTelegramMessageDirect(outMsg)
	m.telegram.testMsgActive = false
	m.telegram.testMsgInput = ""
	return m, nil
}

func (m *Model) renderTelegramForm(width int) string {
	b := &strings.Builder{}
	b.WriteString(titleStyle.Bold(true).Render("  Telegram Configuration\n"))
	b.WriteString(renderDivider(width - 2))
	b.WriteByte('\n')

	tokenLabel := "Bot Token"
	if m.telegram.editingToken {
		tokenLabel += " ◀"
	}
	disp := m.telegram.tokenInput
	if disp == "" {
		disp = subtleStyle.Render("(paste your BotFather token here)")
	} else {
		disp = truncateSingleLine(disp, max(width-24, 16))
	}
	fmt.Fprintf(b, "  %s %s\n", boldStyle.Render(tokenLabel+":"), disp)
	b.WriteString(subtleStyle.Render("  Get token from @BotFather on Telegram\n\n"))

	usersLabel := "Allowed User IDs"
	if m.telegram.editingUsers {
		usersLabel += " ◀"
	}
	usersDisp := m.telegram.allowedUsersInput
	if usersDisp == "" {
		usersDisp = "(optional — comma-separated numeric IDs)"
	} else {
		usersDisp = truncateSingleLine(usersDisp, max(width-35, 16))
	}
	fmt.Fprintf(b, "  %s %s\n", boldStyle.Render(usersLabel+":"), usersDisp)
	b.WriteString(subtleStyle.Render("  Send /id to this bot or use @userinfobot to get your numeric ID\n\n"))

	if m.telegram.saveSuccess {
		b.WriteString(okStyle.Render("  ✓ Saved! Bot connecting…\n\n"))
	} else if m.telegram.saveError != "" {
		fmt.Fprintf(b, "%s\n\n", warnStyle.Render("  ✗ "+m.telegram.saveError))
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
		for p := range strings.SplitSeq(usersRaw, ",") {
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

	previousToken := strings.TrimSpace(m.eng.Config.Telegram.Token)
	m.eng.Config.Telegram.Token = token
	m.eng.Config.Telegram.Enabled = true
	m.eng.Config.Telegram.AllowedUsers = allowedIDs

	savePath := m.telegramConfigPath()
	if err := os.MkdirAll(filepath.Dir(savePath), 0o700); err != nil {
		m.telegram.saveError = fmt.Sprintf("Save failed: %v", err)
		m.appendTelegramLog("Config", m.telegram.saveError, true)
		return m
	}
	if err := m.eng.Config.Save(savePath); err != nil {
		m.telegram.saveError = fmt.Sprintf("Save failed: %v", err)
		m.appendTelegramLog("Config", m.telegram.saveError, true)
		return m
	}

	if m.refreshExistingTelegramBot(previousToken, token, allowedIDs, savePath) {
		m.telegram.saveSuccess = true
		m.telegram.formActive = false
		m.telegram.inputActive = false
		return m
	}

	tgBot, err := bot.New(m.eng.BackgroundContext(), token)
	if err != nil {
		m.telegram.saveError = fmt.Sprintf("Invalid token: %v", err)
		m.appendTelegramLog("Telegram", m.telegram.saveError, true)
		return m
	}
	m.bindTelegramBotToPanel(tgBot)
	tgBot.SetAllowedUsers(allowedIDs)
	tgBot.SetOnMessage(func(userID int64, text string, replyFn func(string)) {
		_ = m.addTelegramMessage(telegramMessageItem{from: fmt.Sprintf("User %d", userID), text: text, time: time.Now().Format("15:04"), isOut: false})
		go func() {
			resp, err := m.eng.Ask(context.Background(), text)
			if err != nil {
				replyFn("⚠️ Error: " + err.Error())
				return
			}
			resp = truncateRunes(resp, 4000, "...")
			_ = m.addTelegramMessage(telegramMessageItem{from: "DFMC", text: resp, time: time.Now().Format("15:04"), isOut: true})
			replyFn(resp)
		}()
	})
	m.bindTelegramBotToPanel(tgBot)

	go func() {
		if err := tgBot.Start(); err != nil {
			enqueueTelegramPanelMessage(m.telegram.events, telegramMessageItem{from: "Log", text: fmt.Sprintf("bot start error: %v", err), time: time.Now().Format("15:04")})
		}
	}()

	if health := tgBot.Health(); health != nil {
		if username, ok := health["bot_username"].(string); ok {
			m.telegram.botUsername = username
		}
	}
	m.stopExistingTelegramBot()
	m.telegram.telegramBot = tgBot
	m.eng.TelegramBot = tgBot
	m.eng.TelegramAllowedUsers = allowedIDs
	m.telegram.saveSuccess = true
	m.telegram.formActive = false
	m.telegram.inputActive = false
	m.appendTelegramLog("Config", fmt.Sprintf("Saved Telegram config to %s", savePath), true)
	m.appendTelegramLog("Telegram", fmt.Sprintf("Connected @%s with %d allowed user(s).", blankFallback(m.telegram.botUsername, "bot"), len(allowedIDs)), true)
	return m
}

func (m *Model) stopExistingTelegramBot() {
	tgBot := m.currentTelegramBot()
	if tgBot == nil {
		return
	}
	tgBot.SetOnMessage(nil)
	tgBot.SetLogger(nil)
	tgBot.Stop()
	if m.telegram.telegramBot == tgBot {
		m.telegram.telegramBot = nil
	}
	if m.eng != nil && m.eng.TelegramBot == tgBot {
		m.eng.TelegramBot = nil
	}
}

func (m *Model) refreshExistingTelegramBot(previousToken, token string, allowedIDs []int64, savePath string) bool {
	if strings.TrimSpace(previousToken) != strings.TrimSpace(token) {
		return false
	}
	existing := m.telegram.telegramBot
	if existing == nil && m.eng != nil {
		existing = m.eng.TelegramBot
	}
	if existing == nil {
		return false
	}
	m.bindTelegramBotToPanel(existing)
	existing.SetAllowedUsers(allowedIDs)
	m.telegram.telegramBot = existing
	if m.eng != nil {
		m.eng.TelegramBot = existing
		m.eng.TelegramAllowedUsers = allowedIDs
	}
	m.appendTelegramLog("Config", fmt.Sprintf("Saved Telegram config to %s", savePath), true)
	m.appendTelegramLog("Telegram", fmt.Sprintf("Allowed users updated: %d user(s).", len(allowedIDs)), true)
	return true
}

func (m *Model) telegramConfigPath() string {
	if path, err := m.userConfigPath(); err == nil && strings.TrimSpace(path) != "" {
		return path
	}
	return filepath.Join(config.UserConfigDir(), "config.yaml")
}

func (m *Model) currentTelegramBot() *bot.TelegramBot {
	if m == nil {
		return nil
	}
	if m.telegram.telegramBot != nil {
		return m.telegram.telegramBot
	}
	if m.eng != nil {
		return m.eng.TelegramBot
	}
	return nil
}

func (m *Model) telegramRegisteredCount() int {
	tgBot := m.currentTelegramBot()
	if tgBot == nil {
		return m.telegram.registeredCount
	}
	count := tgBot.RegisteredUsers()
	m.telegram.registeredCount = count
	if m.telegram.botUsername == "" {
		if health := tgBot.Health(); health != nil {
			if username, ok := health["bot_username"].(string); ok {
				m.telegram.botUsername = username
			}
		}
	}
	return count
}

func (m *Model) disconnectTelegramBot() {
	tgBot := m.currentTelegramBot()
	if tgBot != nil {
		tgBot.SetOnMessage(nil)
		tgBot.SetLogger(nil)
		tgBot.Stop()
	}
	if m.eng != nil {
		if m.eng.Config != nil {
			m.eng.Config.Telegram.Enabled = false
			m.eng.Config.Telegram.Token = ""
			m.eng.Config.Telegram.AllowedUsers = nil
			savePath := m.telegramConfigPath()
			_ = os.MkdirAll(filepath.Dir(savePath), 0o700)
			_ = m.eng.Config.Save(savePath)
		}
		m.eng.TelegramBot = nil
		m.eng.TelegramAllowedUsers = nil
	}
	m.telegram.messages = nil
	m.telegram.botUsername = ""
	m.telegram.registeredCount = 0
	m.telegram.telegramBot = nil
	m.telegram.inputActive = false
	m.telegram.formActive = false
	m.notice = "Telegram bot disconnected"
}

func (m *Model) handleTelegramMessageAdded(msg telegramMessageAddedMsg) (tea.Model, tea.Cmd) {
	m.addTelegramMessageDirect(msg.msg)
	return m, telegramMessageCmd(m.telegram.events)
}

func (m *Model) addTelegramMessage(msg telegramMessageItem) tea.Cmd {
	return func() tea.Msg {
		return telegramMessageAddedMsg{msg: msg}
	}
}

type telegramMessageAddedMsg struct {
	msg telegramMessageItem
}
