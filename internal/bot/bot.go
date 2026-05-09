//go:build !telegram_bot_wip

package bot

import "fmt"

// TelegramBot is the Telegram bot instance (stub for non-WIP builds).
// Actual type is defined in telegram.go when telegram_bot_wip tag is set.
type TelegramBot struct{}

// New is a stub that returns an error for non-WIP builds.
// When telegram_bot_wip is set, the real implementation in telegram.go is used.
func New(token string) (*TelegramBot, error) {
	return nil, fmt.Errorf("telegram: not built with telegram_bot_wip tag")
}

func (b *TelegramBot) SetMessageHandler(fn func(userID int64, text string))        {}
func (b *TelegramBot) Start() error                                                 { return nil }
func (b *TelegramBot) Stop()                                                       {}
func (b *TelegramBot) SendToUser(userID int64, text string) error                  { return nil }
func (b *TelegramBot) SendToChat(chatID int64, text string) error                  { return nil }
func (b *TelegramBot) Broadcast(text string) error                                   { return nil }
func (b *TelegramBot) RegisteredUsers() int                                         { return 0 }
func (b *TelegramBot) Health() map[string]interface{}                               { return nil }