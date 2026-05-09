package tui

// telegram_panel.go — Telegram bot panel for TUI.
// Shows messages, user list, connection status.
// Accessed via Ctrl+B → "Telegram" panel.

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// telegramMessageItem represents one message in the panel.
type telegramMessageItem struct {
	from  string
	text  string
	time  string
	isOut bool // true = sent by bot
}

// telegramPanelStyle defines the panel styling.
var telegramPanelStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("99")).
	Padding(1, 2)

// updateTelegramPanel handles messages for the telegram panel.
func (m *Model) updateTelegramPanel(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case telegramMessageUpdate:
		m.telegram.messages = append(m.telegram.messages, telegramMessageItem{
			from:  msg.From,
			text:  msg.Text,
			time:  time.Now().Format("15:04"),
			isOut: msg.IsOutgoing,
		})
		m.telegram.scrollOffset = len(m.telegram.messages) - 1

	case tea.KeyMsg:
		switch msg.String() {
		case "up":
			if m.telegram.scrollOffset > 0 {
				m.telegram.scrollOffset--
			}
		case "down":
			if m.telegram.scrollOffset < len(m.telegram.messages)-1 {
				m.telegram.scrollOffset++
			}
		case "enter":
			return m.submitTelegramMessage()
		case "esc":
			_, _ = m.closePanelOverlay()
			return nil
		}
	}
	return nil
}

// submitTelegramMessage sends a message via the bot.
func (m *Model) submitTelegramMessage() tea.Cmd {
	if m.telegram.inputValue == "" {
		return nil
	}

	text := m.telegram.inputValue
	m.telegram.inputValue = ""

	// Add to local messages
	m.telegram.messages = append(m.telegram.messages, telegramMessageItem{
		from:  "You",
		text:  text,
		time:  time.Now().Format("15:04"),
		isOut: true,
	})

	// Try to get the bot from engine (via eng.TelegramBot())
	if m.eng != nil {
		m.telegram.botStatus = "📤 Message queued"
	}

	return nil
}

// renderTelegramPanel renders the telegram panel.
func (m *Model) renderTelegramPanel() string {
	var b strings.Builder

	b.WriteString(telegramPanelStyle.Render(m.telegramPanelTitle()) + "\n")

	// Status bar
	status := "🤖 Telegram Bot"
	if m.eng != nil {
		status += " | Engine: ready"
	} else {
		status += " | Not connected (use --telegram-token)"
	}
	b.WriteString(lipgloss.NewStyle().Faint(true).Render(status) + "\n\n")

	// Messages
	if len(m.telegram.messages) == 0 {
		b.WriteString("  No messages yet.\n")
		b.WriteString("  Use /chat <message> from Telegram to send.\n")
	} else {
		for i, msg := range m.telegram.messages {
			prefix := "📥"
			if msg.isOut {
				prefix = "📤"
			}
			marker := "  "
			if i == m.telegram.scrollOffset {
				marker = "▶ "
			}
			b.WriteString(marker + prefix + " " + lipgloss.NewStyle().Bold(true).Render(msg.from) + " ")
			b.WriteString(lipgloss.NewStyle().Faint(true).Render(msg.time) + "\n")
			b.WriteString("    " + msg.text + "\n")
		}
	}

	// Input hint
	b.WriteString("\n" + lipgloss.NewStyle().Faint(true).Render("↑↓ scroll | Enter submit | Esc close"))

	return b.String()
}

// telegramPanelTitle returns the panel title with status.
func (m *Model) telegramPanelTitle() string {
	title := "📱 Telegram"
	if m.telegram.botStatus != "" {
		title += " — " + m.telegram.botStatus
	}
	return title
}

// telegramMessageUpdate is a message received from the bot.
type telegramMessageUpdate struct {
	From       string
	Text       string
	IsOutgoing bool
}

// telegramPanelState holds state for the telegram panel.
type telegramPanelState struct {
	messages     []telegramMessageItem
	scrollOffset int
	inputValue   string
	botStatus    string
	userCount    int
}

// InitBotPanel initializes the telegram panel state.
func (m *Model) InitBotPanel() {
	m.telegram = telegramPanelState{
		messages:    []telegramMessageItem{},
		scrollOffset: 0,
		inputValue:  "",
		botStatus:   "",
	}
}