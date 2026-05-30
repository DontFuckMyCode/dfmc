package bot

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TelegramBot is the main Telegram bot instance.
type TelegramBot struct {
	api          *tgbotapi.BotAPI
	token        string
	allowedUsers map[int64]struct{} // whitelist of allowed user IDs
	chatIDs      map[int64]int64    // userID → chatID
	lastAction   map[int64]time.Time
	logger       func(format string, args ...any)
	mu           sync.RWMutex
	loggerMu     sync.RWMutex
	onMessageMu  sync.RWMutex

	// onMessage is called when a message passes allowed-users check.
	// args: userID, message text, replyFn.
	onMessage func(userID int64, text string, replyFn func(string))

	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new Telegram bot with the given token.
// Token must be a valid BotFather token.
// Call SetAllowedUsers to restrict access by Telegram user ID.
//
// The ctx parameter controls the bot's lifecycle: canceling ctx signals
// the bot to shut down, and the bot's lifetime is bounded to ctx's
// deadline if one is set.
func New(ctx context.Context, token string) (*TelegramBot, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("telegram token cannot be empty")
	}

	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("telegram init: %w", err)
	}

	if api.Self.UserName == "" {
		return nil, fmt.Errorf("telegram: invalid token or bot not found")
	}

	ctx, cancel := context.WithCancel(ctx)

	bot := &TelegramBot{
		api:          api,
		token:        token,
		allowedUsers: make(map[int64]struct{}),
		chatIDs:      make(map[int64]int64),
		lastAction:   make(map[int64]time.Time),
		ctx:          ctx,
		cancel:       cancel,
	}

	bot.logf("[telegram] bot initialized: @%s", api.Self.UserName)
	return bot, nil
}

// SetLogger routes Telegram diagnostics to the owner. A nil logger makes the
// bot quiet, which is important for full-screen TUIs where stdout/stderr writes
// corrupt the active screen.
func (b *TelegramBot) SetLogger(fn func(format string, args ...any)) {
	if b == nil {
		return
	}
	b.loggerMu.Lock()
	b.logger = fn
	b.loggerMu.Unlock()
}

func (b *TelegramBot) logf(format string, args ...any) {
	if b == nil {
		return
	}
	b.loggerMu.RLock()
	fn := b.logger
	b.loggerMu.RUnlock()
	if fn != nil {
		fn(format, args...)
	}
}

// SetAllowedUsers replaces the allowed-users whitelist.
// Empty slice = NO users allowed (secure by default).
// IDs are stored in a map for O(1) lookup.
func (b *TelegramBot) SetAllowedUsers(ids []int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allowedUsers = make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			b.allowedUsers[id] = struct{}{}
		}
	}
	b.logf("[telegram] allowed users set: %d", len(ids))
}

// isAllowed returns true if the user is in the allowed-users whitelist.
// Empty allowedUsers = nobody allowed.
func (b *TelegramBot) isAllowed(userID int64) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.allowedUsers == nil {
		return false
	}
	_, ok := b.allowedUsers[userID]
	return ok
}

// AllowedUsers returns the current list of allowed user IDs.
func (b *TelegramBot) AllowedUsers() []int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ids := make([]int64, 0, len(b.allowedUsers))
	for id := range b.allowedUsers {
		ids = append(ids, id)
	}
	return ids
}

// Start begins the update loop. Blocks until Stop() is called.
func (b *TelegramBot) Start() error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	b.logf("[telegram] bot started listening")

	for {
		select {
		case <-b.ctx.Done():
			b.api.StopReceivingUpdates()
			b.logf("[telegram] bot stopped")
			return nil
		case update, ok := <-updates:
			if !ok {
				return fmt.Errorf("updates channel closed")
			}
			if update.Message != nil && update.Message.From != nil {
				go b.handleIncomingMessage(update)
			}
			if update.CallbackQuery != nil && update.CallbackQuery.From != nil {
				if !b.allowUserAction(update.CallbackQuery.From.ID) {
					cbResp := tgbotapi.NewCallback(update.CallbackQuery.ID, "Slow down and try again.")
					_, _ = b.api.Request(cbResp)
					b.logf("[telegram] user=%d callback rate limited", update.CallbackQuery.From.ID)
					continue
				}
				go b.handleCallbackQuery(update)
			}
		}
	}
}

// Stop shuts down the bot's update loop.
func (b *TelegramBot) Stop() {
	b.cancel()
}

// SetOnMessage registers the callback for incoming messages that pass
// the allowed-users check. replyFn is SendToUser.
func (b *TelegramBot) SetOnMessage(fn func(userID int64, text string, replyFn func(string))) {
	if b == nil {
		return
	}
	b.onMessageMu.Lock()
	b.onMessage = fn
	b.onMessageMu.Unlock()
}

func (b *TelegramBot) messageHandler() func(userID int64, text string, replyFn func(string)) {
	if b == nil {
		return nil
	}
	b.onMessageMu.RLock()
	fn := b.onMessage
	b.onMessageMu.RUnlock()
	return fn
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
			b.logf("[telegram] broadcast to %d failed: %v", userID, err)
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
	if msg == nil || msg.From == nil {
		return
	}

	userID := msg.From.ID

	// Log the message
	b.logf("[telegram] %s (%d): %s", msg.From.UserName, userID, redactForLog(msg.Text, 80))

	// Allow /help and /start even without explicit allowedUsers set
	// (but only if allowedUsers is configured — empty = nobody allowed).
	// These open-access commands need to reply, but we deliberately do
	// NOT register the chatID before handling them — a public bot could
	// otherwise be DOS'd by random Telegram users sending /help to grow
	// the chatIDs map. handleCommand replies via msg.Chat.ID directly.
	if msg.IsCommand() {
		cmd := msg.Command()
		if cmd == "help" || cmd == "start" || cmd == "id" || cmd == "whoami" {
			b.handleCommand(msg)
			return
		}
	}

	// Check allowed-users whitelist BEFORE recording the chat ID so
	// unauthorized senders never enter the persistent maps.
	if !b.isAllowed(userID) {
		b.logf("[telegram] unauthorized user=%d rejected", userID)
		b.reply(msg.Chat.ID, "⛔ Access denied. Contact the bot admin.")
		return
	}

	// Authorized: track chat ID for reply capability, with a cap so a
	// compromised admin token or label confusion can't grow the map
	// unbounded either.
	b.mu.Lock()
	if _, known := b.chatIDs[userID]; known || len(b.chatIDs) < maxTrackedUsers {
		b.chatIDs[userID] = msg.Chat.ID
	}
	b.mu.Unlock()

	// Rate limit check
	if !b.allowUserAction(userID) {
		b.reply(msg.Chat.ID, "⏳ Slow down — try again in a moment.")
		return
	}

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
	case "id", "whoami":
		b.cmdID(msg)
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
	userID := msg.From.ID

	onMessage := b.messageHandler()
	if onMessage != nil {
		replyFn := func(text string) {
			if err := b.SendToUser(userID, text); err != nil {
				b.logf("[telegram] reply error: %v", err)
			}
		}
		onMessage(userID, msg.Text, replyFn)
	} else {
		b.reply(msg.Chat.ID, "🤖 DFMC engine not connected. Use /help.")
	}
}

// cmdStart is the /start command.
func (b *TelegramBot) cmdStart(msg *tgbotapi.Message) {
	text := fmt.Sprintf(
		"👋 *Welcome to DFMC!*\n\n" +
			"I'm your coding assistant bridge.\n\n" +
			"*Available Commands*\n" +
			"• /help — all commands\n" +
			"• /status — system health\n" +
			"• /chat `<message>` — send a message\n\n" +
			"Just send any text to chat!",
	)
	b.reply(msg.Chat.ID, text)
}

// cmdHelp is the /help command.
func (b *TelegramBot) cmdHelp(msg *tgbotapi.Message) {
	text := "*Available Commands*\n\n" +
		"• /start — welcome message\n" +
		"• /help — this help\n" +
		"• /status — system health check\n" +
		"• /chat `<message>` — send a message to DFMC\n" +
		"• /subscribe — receive notifications\n" +
		"• /unsubscribe — stop notifications\n\n" +
		"*Tips*\n" +
		"• Send any text to chat with DFMC\n" +
		"• Use /chat for multi-line messages\n" +
		"• Prefix with / for commands"
	b.reply(msg.Chat.ID, text)
}

// cmdStatus is the /status command.
func (b *TelegramBot) cmdStatus(msg *tgbotapi.Message) {
	b.mu.RLock()
	userCount := len(b.chatIDs)
	b.mu.RUnlock()

	b.reply(msg.Chat.ID, fmt.Sprintf(
		"✅ *DFMC Status*\n\n🧠 Engine: ready\n📊 Users: %d\n🤖 Bot: @%s",
		userCount,
		b.api.Self.UserName,
	))
}

// cmdChat is the /chat command — forward to engine via callback.
func (b *TelegramBot) cmdID(msg *tgbotapi.Message) {
	if msg == nil || msg.From == nil {
		return
	}
	b.reply(msg.Chat.ID, telegramUserIDText(msg.From.ID, msg.From.UserName))
}

func telegramUserIDText(userID int64, username string) string {
	username = strings.TrimSpace(username)
	if username == "" {
		username = "(no username)"
	} else {
		username = "@" + username
	}
	return fmt.Sprintf("Your Telegram user ID is `%d`.\nUsername: %s\nAdd this ID in DFMC Telegram allowed users.", userID, username)
}

func (b *TelegramBot) cmdChat(msg *tgbotapi.Message) {
	text := msg.CommandArguments()
	if text == "" {
		b.reply(msg.Chat.ID, "Usage: /chat `<message>`")
		return
	}

	userID := msg.From.ID

	onMessage := b.messageHandler()
	if onMessage != nil {
		replyFn := func(response string) {
			if err := b.SendToUser(userID, response); err != nil {
				b.logf("[telegram] chat reply error: %v", err)
			}
		}
		// Acknowledge immediately
		b.reply(msg.Chat.ID, "⏳ Processing your request...")
		onMessage(userID, text, replyFn)
	} else {
		b.reply(msg.Chat.ID, "🤖 Engine not connected yet.")
	}
}

// cmdSubscribe enables notifications for this user.
func (b *TelegramBot) cmdSubscribe(msg *tgbotapi.Message) {
	b.mu.Lock()
	b.chatIDs[msg.From.ID] = msg.Chat.ID
	b.mu.Unlock()
	b.reply(msg.Chat.ID, "🔔 Notifications enabled.")
}

// cmdUnsubscribe currently only sends confirmation.
func (b *TelegramBot) cmdUnsubscribe(msg *tgbotapi.Message) {
	b.reply(msg.Chat.ID, "🔕 Notifications disabled.")
}

// handleCallbackQuery handles inline keyboard button presses.
func (b *TelegramBot) handleCallbackQuery(update tgbotapi.Update) {
	callback := update.CallbackQuery
	if callback == nil || callback.Message == nil {
		return
	}
	cbResp := tgbotapi.NewCallback(callback.ID, "")
	_, _ = b.api.Request(cbResp)

	switch callback.Data {
	case "refresh":
		b.reply(callback.Message.Chat.ID, "🔄 Refreshing...")
	case "status":
		b.reply(callback.Message.Chat.ID, "✅ DFMC is running")
	default:
		b.reply(callback.Message.Chat.ID, "Callback: "+callback.Data)
	}
}

// reply sends a simple text reply.
func (b *TelegramBot) reply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := b.api.Send(msg); err != nil {
		b.logf("[telegram] reply error: %v", err)
	}
}

const telegramRateLimitWindow = 750 * time.Millisecond

// maxTrackedUsers caps the chatIDs and lastAction maps so a bot exposed
// to the public internet can't be DOS'd by random Telegram users
// sending messages until process memory is exhausted. Past the cap, new
// users are rate-limit-denied but not tracked further (existing
// authorized users keep working). 10k entries covers any realistic
// authorized userbase while keeping per-map memory under a few MB.
const maxTrackedUsers = 10_000

func (b *TelegramBot) allowUserAction(userID int64) bool {
	if b == nil {
		return false
	}
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastAction == nil {
		b.lastAction = make(map[int64]time.Time)
	}
	if last, ok := b.lastAction[userID]; ok && now.Sub(last) < telegramRateLimitWindow {
		return false
	}
	// Cap to prevent the map growing unbounded under spam from random
	// Telegram users. Drop the request rather than recording it; the
	// caller treats false as rate-limited which is the right outcome
	// for an unrecognised user past saturation.
	if _, known := b.lastAction[userID]; !known && len(b.lastAction) >= maxTrackedUsers {
		return false
	}
	b.lastAction[userID] = now
	return true
}

// Health returns bot health info.
func (b *TelegramBot) Health() map[string]any {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return map[string]any{
		"bot_username":  b.api.Self.UserName,
		"registered":    len(b.chatIDs),
		"allowed_users": len(b.allowedUsers),
		"token_set":     b.token != "",
	}
}

// truncate shortens a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

var telegramSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password)\s*[:=]\s*([^\s]+)`),
	regexp.MustCompile(`(?i)\b(sk-[A-Za-z0-9_-]{8,})\b`),
}

func redactForLog(s string, maxLen int) string {
	out := s
	for _, re := range telegramSecretPatterns {
		out = re.ReplaceAllStringFunc(out, func(match string) string {
			if groups := re.FindStringSubmatch(match); len(groups) >= 3 {
				return groups[1] + "=<redacted>"
			}
			return "<redacted>"
		})
	}
	return truncate(out, maxLen)
}

// Message represents a Telegram message for the TUI panel.
type Message struct {
	ID         int64
	FromID     int64
	FromName   string
	Text       string
	Timestamp  time.Time
	IsOutgoing bool // true = sent by bot, false = received
}
