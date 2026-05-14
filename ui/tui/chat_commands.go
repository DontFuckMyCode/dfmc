package tui

// Slash-command dispatcher for the chat panel. executeChatCommand owns the
// parse/error guard, then delegates command families to small routers so new
// commands do not keep stretching one giant switch.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) executeChatCommand(raw string) (tea.Model, tea.Cmd, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "/") {
		return m, nil, false
	}
	cmd, args, rawArgs, err := parseChatCommandInput(raw)
	if err != nil {
		m.notice = "command parse: " + err.Error()
		return m.appendSystemMessage("Command parse error: " + err.Error()), nil, true
	}
	if cmd == "" {
		m.notice = "Slash command is empty."
		return m.appendSystemMessage("Slash command is empty. Try /help."), nil, true
	}

	if next, teaCmd, handled := m.executeSessionSlashCommand(cmd, args); handled {
		return next, teaCmd, true
	}
	if next, teaCmd, handled := m.executePanelSlashCommand(cmd, args, rawArgs); handled {
		return next, teaCmd, true
	}
	if next, teaCmd, handled := m.executeAssistantSlashCommand(cmd, args, raw); handled {
		return next, teaCmd, true
	}
	if next, teaCmd, handled := m.executeUtilitySlashCommand(cmd, args); handled {
		return next, teaCmd, true
	}
	return m.executeUnknownSlashCommand(cmd, args, raw)
}
