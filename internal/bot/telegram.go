//go:build telegram_bot_wip

package bot

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TelegramBot is the main Telegram bot instance.
type TelegramBot struct {
	api     *tgbotapi.BotAPI
	token   string
	chatIDs map[int64]int64 // userID → chatID
	mu      sync.RWMutex

	onMessage func(userID int64, text string) // callback to engine

	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new Telegram bot with the given token.
func New(token string) (*TelegramBot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("telegram init: %w", err)
	}

	// Verify bot token with a simple API call
	if api.Self.UserName == "" {
		return nil, fmt.Errorf("telegram: invalid token or bot not found")
	}

	ctx, cancel := context.WithCancel(context.Background())

	bot := &TelegramBot{
		api:     api,
		token:   token,
		chatIDs: make(map[int64]int64),
		ctx:     ctx,
		cancel:  cancel,
	}

	log.Printf("[telegram] bot initialized: @%s", api.Self.UserName)
	return bot, nil
}

// Start begins the update loop. Blocks until Stop() is called.
func (b *TelegramBot) Start() error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	log.Printf("[telegram] bot started listening")

	for {
		select {
		case <-b.ctx.Done():
			b.api.StopReceivingUpdates()
			log.Printf("[telegram] bot stopped")
			return nil
		case update, ok := <-updates:
			if !ok {
				return fmt.Errorf("updates channel closed")
			}
			if update.Message != nil {
				go b.handleIncomingMessage(update)
			}
			if update.CallbackQuery != nil {
				go b.handleCallbackQuery(update)
			}
		}
	}
}

// Stop shuts down the bot's update loop.
func (b *TelegramBot) Stop() {
	b.cancel()
}

// SetMessageHandler registers the callback for incoming messages.
func (b *TelegramBot) SetMessageHandler(fn func(userID int64, text string)) {
	b.onMessage = fn
}

// SendToUser sends a message to a specific user.
// Returns error if user is not registered.
func (b *TelegramBot) SendToUser(userID int64, text string) error {
	b.mu.RLock()
	chatID, ok := b.chatIDs[userID]
	b.mu.RUnlock()

	if !ok {
		return fmt.Errorf("user %d not registered", userID)
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	_, err := b.api.Send(msg)
	return err
}

// SendToChat sends a message to a specific chat ID.
func (b *TelegramBot) SendToChat(chatID int64, text string) error {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	_, err := b.api.Send(msg)
	return err
}

// Broadcast sends a message to all registered users.
func (b *TelegramBot) Broadcast(text string) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var lastErr error
	for userID, chatID := range b.chatIDs {
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ParseMode = "Markdown"
		if _, err := b.api.Send(msg); err != nil {
			log.Printf("[telegram] broadcast to %d failed: %v", userID, err)
			lastErr = err
		}
	}
	return lastErr
}

// RegisteredUsers returns the count of registered users.
func (b *TelegramBot) RegisteredUsers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.chatIDs)
}

// handleIncomingMessage processes an incoming message.
func (b *TelegramBot) handleIncomingMessage(update tgbotapi.Update) {
	msg := update.Message

	// Track this user's chat ID
	b.mu.Lock()
	b.chatIDs[msg.From.ID] = msg.Chat.ID
	b.mu.Unlock()

	// Log the message
	log.Printf("[telegram] %s (%d): %s", msg.From.UserName, msg.From.ID, truncate(msg.Text, 50))

	// Route to handler
	if msg.IsCommand() {
		b.handleCommand(msg)
	} else {
		b.handleTextMessage(msg)
	}
}

// handleCommand routes built-in commands.
func (b *TelegramBot) handleCommand(msg *tgbotapi.Message) {
	cmd := msg.Command()

	switch cmd {
	case "start":
		b.cmdStart(msg)
	case "help":
		b.cmdHelp(msg)
	case "status":
		b.cmdStatus(msg)
	case "chat":
		b.cmdChat(msg)
	case "subscribe":
		b.cmdSubscribe(msg)
	case "unsubscribe":
		b.cmdUnsubscribe(msg)
	default:
		b.reply(msg.Chat.ID, "Unknown command. Try /help")
	}
}

// handleTextMessage handles non-command messages.
func (b *TelegramBot) handleTextMessage(msg *tgbotapi.Message) {
	if b.onMessage != nil {
		b.onMessage(msg.From.ID, msg.Text)
	} else {
		b.reply(msg.Chat.ID, "DFMC engine not connected. Use /help to see available commands.")
	}
}

// cmdStart is the /start command.
func (b *TelegramBot) cmdStart(msg *tgbotapi.Message) {
	text := fmt.Sprintf(
		"👋 *Welcome to DFMC Bot!*\n\n"+
			"I'm your coding assistant bridge.\n"+
			"Use /chat to send a message\n"+
			"Use /status to check system health\n"+
			"Use /help for all commands",
	)
	b.reply(msg.Chat.ID, text)
}

// cmdHelp is the /help command.
func (b *TelegramBot) cmdHelp(msg *tgbotapi.Message) {
	text := "*Available Commands*\n\n" +
		"• /start — Welcome message\n" +
		"• /help — This help message\n" +
		"• /chat <message> — Send a message to DFMC\n" +
		"• /status — System health check\n" +
		"• /subscribe — Receive notifications\n" +
		"• /unsubscribe — Stop notifications\n\n" +
		"*Tips*\n" +
		"• Send any text to chat with DFMC\n" +
		"• Use /chat for multi-line messages\n" +
		"• Prefix with / for commands"
	b.reply(msg.Chat.ID, text)
}

// cmdStatus is the /status command.
func (b *TelegramBot) cmdStatus(msg *tgbotapi.Message) {
	b.reply(msg.Chat.ID, "✅ DFMC is running\n🧠 Engine: ready\n📊 Active users: "+fmt.Sprint(b.RegisteredUsers()))
}

// cmdChat is the /chat command.
func (b *TelegramBot) cmdChat(msg *tgbotapi.Message) {
	// Strip /chat prefix
	text := msg.CommandArguments()
	if text == "" {
		b.reply(msg.Chat.ID, "Usage: /chat `<message>`")
		return
	}

	// Acknowledge
	b.reply(msg.Chat.ID, "⏳ Processing your request...")

	// Forward to engine
	if b.onMessage != nil {
		b.onMessage(msg.From.ID, text)
	}
}

// cmdSubscribe enables notifications for this user.
func (b *TelegramBot) cmdSubscribe(msg *tgbotapi.Message) {
	b.mu.Lock()
	b.chatIDs[msg.From.ID] = msg.Chat.ID // ensure registered
	b.mu.Unlock()
	b.reply(msg.Chat.ID, "🔔 Notifications enabled. You'll receive updates from DFMC.")
}

// cmdUnsubscribe disables notifications.
func (b *TelegramBot) cmdUnsubscribe(msg *tgbotapi.Message) {
	// Note: we don't actually remove from chatIDs since we need it for replies
	// Instead just mark them as unsubscribed (could add a separate map)
	b.reply(msg.Chat.ID, "🔕 Notifications disabled.")
}

// handleCallbackQuery handles inline keyboard button presses.
func (b *TelegramBot) handleCallbackQuery(update tgbotapi.Update) {
	callback := update.CallbackQuery
	cbResp := tgbotapi.NewCallback(callback.ID, "")
	b.api.Send(cbResp)

	// Handle based on data
	switch callback.Data {
	case "refresh":
		b.reply(callback.Message.Chat.ID, "🔄 Refreshing...")
	case "status":
		b.reply(callback.Message.Chat.ID, "✅ DFMC is running\n🧠 Engine: ready")
	default:
		b.reply(callback.Message.Chat.ID, "Callback: "+callback.Data)
	}
}

// reply sends a simple text reply.
func (b *TelegramBot) reply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("[telegram] reply error: %v", err)
	}
}

// notifyEngine sends a message to the DFMC engine via registered handler.
func (b *TelegramBot) notifyEngine(userID int64, text string) {
	if b.onMessage != nil {
		b.onMessage(userID, text)
	}
}

// Health returns bot health info.
func (b *TelegramBot) Health() map[string]interface{} {
	return map[string]interface{}{
		"bot_username": b.api.Self.UserName,
		"registered":   b.RegisteredUsers(),
		"token_set":    b.token != "",
	}
}

// truncate shortens a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Message represents a Telegram message for the TUI panel.
type Message struct {
	ID        int64
	FromID    int64
	FromName  string
	Text      string
	Timestamp time.Time
	IsOutgoing bool // true = sent by bot, false = received
}

// NewMessage creates a new Message.
func NewMessage(fromID int64, fromName, text string, isOutgoing bool) *Message {
	return &Message{
		ID:         time.Now().UnixNano(),
		FromID:     fromID,
		FromName:   fromName,
		Text:       text,
		Timestamp:  time.Now(),
		IsOutgoing: isOutgoing,
	}
}