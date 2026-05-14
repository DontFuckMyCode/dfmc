package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) executeSessionSlashCommand(cmd string, args []string) (tea.Model, tea.Cmd, bool) {
	switch cmd {
	case "help":
		return m.handleHelpSlash(args)
	case "quit", "exit", "q":
		m.chat.input = ""
		m.notice = "Goodbye."
		return m.appendSystemMessage("Exiting DFMC \u2014 goodbye."), tea.Quit, true
	case "clear":
		return m.handleClearSlash()
	case "export", "save":
		return m.handleExportSlash(cmd, args)
	case "pin", "unpin":
		return m.handlePinSlash(cmd, args)
	case "fork":
		return m.handleForkSlash(args)
	case "retry":
		return m.handleRetrySlash()
	case "edit":
		return m.handleEditSlash()
	case "file", "files":
		return m.handleFilePickerSlash()
	case "plan":
		return m.handlePlanSlash()
	case "code":
		return m.handleCodeSlash()
	case "drive":
		return m.handleDriveSlash(args)
	case "compact":
		return m.handleCompactSlash(args)
	default:
		return m, nil, false
	}
}

func (m Model) executePanelSlashCommand(cmd string, args []string, rawArgs string) (tea.Model, tea.Cmd, bool) {
	switch cmd {
	case "approve", "approvals", "permissions",
		"hooks", "stats", "tokens", "cost",
		"workflow", "todos", "todo",
		"tasks", "subagents", "workers", "queue", "toolstatus",
		"keylog", "coach", "hints", "intent",
		"copy", "yank", "mouse", "select",
		"status", "log", "calls", "reload",
		"cancel", "abort", "stop",
		"shortcuts", "keys", "cheatsheet":
		return m.runPanelCommand(cmd, args)
	case "context":
		return m.handleContextSlash(args)
	case "tools":
		return m.handleToolsSlash(args)
	case "tool":
		return m.handleToolSlash(args, rawArgs)
	case "ls", "read", "grep", "run", "diff", "patch", "undo", "apply":
		return m.runFileCommand(cmd, args)
	case "providers", "provider", "models", "model":
		return m.runProviderCommand(cmd, args)
	case "key", "apikey", "apikeys":
		return m.runKeyCommand(args)
	default:
		return m, nil, false
	}
}

func (m Model) executeAssistantSlashCommand(cmd string, args []string, raw string) (tea.Model, tea.Cmd, bool) {
	switch cmd {
	case "ask":
		return m.handleAskSlash(args)
	case "chat":
		m.chat.input = ""
		m.notice = "Already in chat. Just type your message."
		return m.appendSystemMessage("You're already in the chat tab \u2014 type your message and press enter."), nil, true
	case "continue", "resume":
		return m.handleContinueSlash(args)
	case "split":
		return m.handleSplitSlash(args)
	case "btw":
		return m.handleBtwSlash(args)
	case "review", "explain", "refactor", "test", "doc":
		return m.runTemplateSlash(cmd, args, raw)
	case "analyze":
		m.chat.input = ""
		return m.runAnalyzeSlash(args, false), nil, true
	case "scan":
		m.chat.input = ""
		return m.runAnalyzeSlash(args, true), nil, true
	default:
		return m, nil, false
	}
}

func (m Model) executeUtilitySlashCommand(cmd string, args []string) (tea.Model, tea.Cmd, bool) {
	switch cmd {
	case "map":
		m.chat.input = ""
		return m.appendSystemMessage(m.codemapSummary()), nil, true
	case "version":
		m.chat.input = ""
		return m.appendSystemMessage(m.versionSummary()), nil, true
	case "doctor", "health":
		m.chat.input = ""
		return m.appendSystemMessage(m.describeHealth()), loadStatusCmd(m.eng), true
	case "setup":
		m.chat.input = ""
		if len(args) > 0 && strings.EqualFold(strings.TrimSpace(args[0]), "clean") {
			report := m.cleanProjectProvidersBlock()
			return m.appendSystemMessage(report), loadStatusCmd(m.eng), true
		}
		return m.appendSystemMessage(m.providerSetupSummary()), nil, true
	case "magicdoc", "magic":
		m.chat.input = ""
		return m.appendSystemMessage(m.magicDocSlash(args)), nil, true
	case "conversation", "conv":
		m.chat.input = ""
		return m.appendSystemMessage(m.conversationSlash(args)), nil, true
	case "memory":
		m.chat.input = ""
		return m.appendSystemMessage(m.memorySlash(args)), nil, true
	case "prompt":
		m.chat.input = ""
		return m.appendSystemMessage(m.promptSlash(args)), nil, true
	case "skill":
		m.chat.input = ""
		return m.appendSystemMessage(m.skillSlash(args)), nil, true
	case "agents", "agent":
		m.chat.input = ""
		return m.appendSystemMessage(m.agentsSlash(args)), nil, true
	case "init", "completion", "man", "serve", "remote", "plugin", "config",
		"debug", "generate", "onboard", "audit", "mcp", "update", "tui":
		return m.executeCLIOnlySlashCommand(cmd, args)
	default:
		return m, nil, false
	}
}

func (m Model) executeCLIOnlySlashCommand(cmd string, args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	m.notice = "/" + cmd + ": run from CLI (not available in TUI)."
	suffix := ""
	if len(args) > 0 {
		suffix = " " + strings.Join(args, " ")
	}
	return m.appendSystemMessage("/" + cmd + " is a CLI command. Run: dfmc " + cmd + suffix), nil, true
}

func (m Model) executeUnknownSlashCommand(cmd string, _ []string, raw string) (tea.Model, tea.Cmd, bool) {
	if suggestion := suggestSlashCommand(cmd); suggestion != "" {
		m.notice = "Unknown /" + cmd + " \u2014 try " + suggestion
		return m.appendSystemMessage("Unknown command: /" + cmd + "\nDid you mean " + suggestion + "?  Run /help for the full list."), nil, true
	}
	m.notice = "Unknown chat command: " + raw
	return m.appendSystemMessage("Unknown chat command: " + raw + "\nRun /help for the catalog."), nil, true
}
