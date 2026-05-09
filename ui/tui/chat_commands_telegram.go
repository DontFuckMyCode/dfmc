package tui

// chat_commands_telegram.go — `/telegram` slash command.
//
//   /telegram                    — show current telegram settings
//   /telegram enable             — set enabled: true
//   /telegram disable            — set enabled: false
//   /telegram token <TOKEN>      — set bot token
//   /telegram allow <USER_ID>    — add a Telegram user ID to allowed list
//   /telegram deny <USER_ID>     — remove a user ID from allowed list
//   /telegram session <NAME>     — set the session/panel name
//
// All writes go to ~/.dfmc/config.yaml (user-home, survives across projects).
// After a successful mutation we reload the engine so the bot picks up
// the new config without a TUI restart.

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func (m Model) runTelegramCommand(args []string) (tea.Model, tea.Cmd, bool) {
	if len(args) == 0 || strings.EqualFold(strings.TrimSpace(args[0]), "list") {
		return m.appendSystemMessage(m.telegramStatusSummary()), nil, true
	}

	sub := strings.ToLower(strings.TrimSpace(args[0]))
	rest := args[1:]

	switch sub {
	case "enable":
		return m.telegramSetEnabled(true)
	case "disable":
		return m.telegramSetEnabled(false)
	case "token":
		if len(rest) == 0 {
			m.notice = "/telegram token needs <BOT_TOKEN>"
			return m.appendSystemMessage("Usage: /telegram token <BOT_TOKEN>\nExample: /telegram token 123456:ABC-DEF...\n\nGet your token from @BotFather on Telegram."), nil, true
		}
		token := strings.TrimSpace(strings.Join(rest, " "))
		return m.telegramSetToken(token)
	case "allow":
		if len(rest) == 0 {
			m.notice = "/telegram allow needs <USER_ID>"
			return m.appendSystemMessage("Usage: /telegram allow <USER_ID>\nExample: /telegram allow 123456789\n\nYour Telegram numeric chat ID — send /start to the bot and check logs, or use @userinfobot."), nil, true
		}
		id, err := parseUserID(rest[0])
		if err != nil {
			m.notice = "/telegram allow: " + err.Error()
			return m.appendSystemMessage("Invalid user ID: " + rest[0] + " — must be a numeric Telegram user ID."), nil, true
		}
		return m.telegramAllowUser(id)
	case "deny":
		if len(rest) == 0 {
			m.notice = "/telegram deny needs <USER_ID>"
			return m.appendSystemMessage("Usage: /telegram deny <USER_ID>"), nil, true
		}
		id, err := parseUserID(rest[0])
		if err != nil {
			m.notice = "/telegram deny: " + err.Error()
			return m.appendSystemMessage("Invalid user ID: " + rest[0]), nil, true
		}
		return m.telegramDenyUser(id)
	case "session":
		if len(rest) == 0 {
			m.notice = "/telegram session needs <NAME>"
			return m.appendSystemMessage("Usage: /telegram session <NAME>\nExample: /telegram session dfmc-home\nThis is the name shown in the Telegram bot panel."), nil, true
		}
		name := strings.TrimSpace(strings.Join(rest, " "))
		return m.telegramSetSessionName(name)
	default:
		m.notice = "unknown /telegram subcommand: " + sub
		return m.appendSystemMessage(m.telegramStatusSummary()), nil, true
	}
}

func (m Model) telegramStatusSummary() string {
	var b strings.Builder

	cfg := m.effectiveTelegramConfig()

	b.WriteString("Telegram settings\n")
	b.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	if cfg.Enabled {
		b.WriteString("Enabled  : ✅ yes\n")
	} else {
		b.WriteString("Enabled  : ❌ no\n")
	}

	if cfg.Token != "" {
		b.WriteString(fmt.Sprintf("Token    : %s\n", maskTGToken(cfg.Token)))
	} else {
		b.WriteString("Token    : — (not set)\n")
	}

	b.WriteString(fmt.Sprintf("Session  : %s\n", cfg.SessionName))
	if cfg.SessionName == "" {
		b.WriteString("          (default: dfmc)\n")
	}

	if len(cfg.AllowedUsers) == 0 {
		b.WriteString("Users    : — (none allowed)\n")
	} else {
		b.WriteString(fmt.Sprintf("Users    : %d authorized\n", len(cfg.AllowedUsers)))
		for _, id := range cfg.AllowedUsers {
			b.WriteString(fmt.Sprintf("          · %d\n", id))
		}
	}

	b.WriteString("\nBuild tag required : go build -tags telegram_bot_wip\n")
	b.WriteString("CLI token override : --telegram-token <TOKEN>\n")
	b.WriteString("\nSubcommands:\n")
	b.WriteString("  /telegram enable|disable\n")
	b.WriteString("  /telegram token <BOT_TOKEN>\n")
	b.WriteString("  /telegram allow|deny <USER_ID>\n")
	b.WriteString("  /telegram session <NAME>\n")

	return b.String()
}

// effectiveTelegramConfig returns the TelegramConfig from the in-memory
// engine config (the merged/user-global result).
func (m Model) effectiveTelegramConfig() config.TelegramConfig {
	if m.eng != nil {
		return m.eng.Config.Telegram
	}
	return config.DefaultConfig().Telegram
}

func (m Model) telegramSetEnabled(enabled bool) (tea.Model, tea.Cmd, bool) {
	path, err := m.userConfigPath()
	if err != nil {
		m.notice = "/telegram enable failed: " + err.Error()
		return m.appendSystemMessage("Cannot resolve user config path: " + err.Error()), nil, true
	}

	cfg, err := m.readUserConfigForTelegram(path)
	if err != nil {
		m.notice = "/telegram enable failed: " + err.Error()
		return m.appendSystemMessage("Failed to load config: " + err.Error()), nil, true
	}

	cfg.Telegram.Enabled = enabled
	if err := cfg.Save(path); err != nil {
		m.notice = "/telegram enable failed: " + err.Error()
		return m.appendSystemMessage("Failed to save: " + err.Error()), nil, true
	}

	reloadHint := ""
	if err := m.reloadEngineConfig(); err != nil {
		reloadHint = fmt.Sprintf("\n(reload warning: %s)", err.Error())
	}

	verb := "enabled"
	if !enabled {
		verb = "disabled"
	}
	m.notice = "Telegram " + verb
	return m.appendSystemMessage(fmt.Sprintf("Telegram %s. Config saved to %s.%s", verb, displayConfigPath(path), reloadHint)), loadStatusCmd(m.eng), true
}

func (m Model) telegramSetToken(token string) (tea.Model, tea.Cmd, bool) {
	path, err := m.userConfigPath()
	if err != nil {
		m.notice = "/telegram token failed: " + err.Error()
		return m.appendSystemMessage("Cannot resolve user config path: " + err.Error()), nil, true
	}

	cfg, err := m.readUserConfigForTelegram(path)
	if err != nil {
		m.notice = "/telegram token failed: " + err.Error()
		return m.appendSystemMessage("Failed to load config: " + err.Error()), nil, true
	}

	cfg.Telegram.Token = token
	if err := cfg.Save(path); err != nil {
		m.notice = "/telegram token failed: " + err.Error()
		return m.appendSystemMessage("Failed to save: " + err.Error()), nil, true
	}

	if err := m.reloadEngineConfig(); err != nil {
		m.notice = "/telegram token saved (reload failed: " + err.Error() + ")"
	} else {
		m.notice = "Telegram token saved"
	}

	return m.appendSystemMessage(fmt.Sprintf(
		"Telegram token saved → %s (masked: %s).\n\nRestart the TUI or send --telegram-token to activate the new token.",
		displayConfigPath(path), maskTGToken(token),
	)), loadStatusCmd(m.eng), true
}

func (m Model) telegramAllowUser(id int64) (tea.Model, tea.Cmd, bool) {
	path, err := m.userConfigPath()
	if err != nil {
		m.notice = "/telegram allow failed: " + err.Error()
		return m.appendSystemMessage("Cannot resolve user config path: " + err.Error()), nil, true
	}

	cfg, err := m.readUserConfigForTelegram(path)
	if err != nil {
		m.notice = "/telegram allow failed: " + err.Error()
		return m.appendSystemMessage("Failed to load config: " + err.Error()), nil, true
	}

	for _, existing := range cfg.Telegram.AllowedUsers {
		if existing == id {
			m.notice = fmt.Sprintf("User %d already in allowed list", id)
			return m.appendSystemMessage(fmt.Sprintf("User %d is already in the allowed list — nothing to add.", id)), nil, true
		}
	}

	cfg.Telegram.AllowedUsers = append(cfg.Telegram.AllowedUsers, id)
	if err := cfg.Save(path); err != nil {
		m.notice = "/telegram allow failed: " + err.Error()
		return m.appendSystemMessage("Failed to save: " + err.Error()), nil, true
	}

	_ = m.reloadEngineConfig()
	m.notice = fmt.Sprintf("User %d added to allowed list", id)
	return m.appendSystemMessage(fmt.Sprintf("User %d added to allowed Telegram users → %s", id, displayConfigPath(path))), loadStatusCmd(m.eng), true
}

func (m Model) telegramDenyUser(id int64) (tea.Model, tea.Cmd, bool) {
	path, err := m.userConfigPath()
	if err != nil {
		m.notice = "/telegram deny failed: " + err.Error()
		return m.appendSystemMessage("Cannot resolve user config path: " + err.Error()), nil, true
	}

	cfg, err := m.readUserConfigForTelegram(path)
	if err != nil {
		m.notice = "/telegram deny failed: " + err.Error()
		return m.appendSystemMessage("Failed to load config: " + err.Error()), nil, true
	}

	newList := make([]int64, 0, len(cfg.Telegram.AllowedUsers))
	found := false
	for _, existing := range cfg.Telegram.AllowedUsers {
		if existing == id {
			found = true
			continue
		}
		newList = append(newList, existing)
	}

	if !found {
		m.notice = fmt.Sprintf("User %d not in allowed list", id)
		return m.appendSystemMessage(fmt.Sprintf("User %d was not in the allowed list — nothing to remove.", id)), nil, true
	}

	cfg.Telegram.AllowedUsers = newList
	if err := cfg.Save(path); err != nil {
		m.notice = "/telegram deny failed: " + err.Error()
		return m.appendSystemMessage("Failed to save: " + err.Error()), nil, true
	}

	_ = m.reloadEngineConfig()
	m.notice = fmt.Sprintf("User %d removed from allowed list", id)
	return m.appendSystemMessage(fmt.Sprintf("User %d removed from allowed Telegram users → %s", id, displayConfigPath(path))), loadStatusCmd(m.eng), true
}

func (m Model) telegramSetSessionName(name string) (tea.Model, tea.Cmd, bool) {
	path, err := m.userConfigPath()
	if err != nil {
		m.notice = "/telegram session failed: " + err.Error()
		return m.appendSystemMessage("Cannot resolve user config path: " + err.Error()), nil, true
	}

	cfg, err := m.readUserConfigForTelegram(path)
	if err != nil {
		m.notice = "/telegram session failed: " + err.Error()
		return m.appendSystemMessage("Failed to load config: " + err.Error()), nil, true
	}

	cfg.Telegram.SessionName = name
	if err := cfg.Save(path); err != nil {
		m.notice = "/telegram session failed: " + err.Error()
		return m.appendSystemMessage("Failed to save: " + err.Error()), nil, true
	}

	_ = m.reloadEngineConfig()
	m.notice = "Telegram session name set to: " + name
	return m.appendSystemMessage(fmt.Sprintf("Telegram session name set to %q → %s", name, displayConfigPath(path))), loadStatusCmd(m.eng), true
}

// readUserConfigForTelegram loads the user config YAML, ensuring the
// telegram: key exists (creating it if absent), and returns the mutated
// config object ready for Save().
func (m Model) readUserConfigForTelegram(path string) (*config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Brand-new user config — start with defaults and add telegram
			cfg := config.DefaultConfig()
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	doc := map[string]any{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	// Ensure telegram: {} exists
	if _, ok := doc["telegram"]; !ok {
		doc["telegram"] = map[string]any{}
	}

	// Re-marshal so we can unmarshal into config.Config
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("re-marshal: %w", err)
	}

	cfg := &config.Config{}
	if err := yaml.Unmarshal(out, cfg); err != nil {
		return nil, fmt.Errorf("unmarshal into Config: %w", err)
	}

	return cfg, nil
}

func parseUserID(s string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(s, "%d", &id)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid user id %q: must be a positive integer", s)
	}
	return id, nil
}

func maskTGToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "—"
	}
	// Show last 8 chars, masked prefix
	if len(token) <= 8 {
		return strings.Repeat("•", len(token))
	}
	return strings.Repeat("•", min(len(token)-8, 10)) + token[len(token)-8:]
}