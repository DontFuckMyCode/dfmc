// Slash-command dispatcher for the chat panel. Extracted from tui.go to keep
// the model file focused on layout/state. executeChatCommand is the single
// entry point: it parses the raw line, routes to the matching handler
// (/help, /clear, /branch, /provider, /quit, /tools, /agent, /plan, …),
// and returns (next model, optional cmd, handled-bool). Handlers may both
// mutate the transcript and queue a tea.Cmd (e.g. quit, restart engine).
//
// Adding a new slash command:
//   1. Add it to the switch on `cmd` below
//   2. Wire the help/catalog entry in describe.go (slashCommandCatalog)
//   3. If it has args, extend the parseChatCommandInput contract
//
// All command handlers must return (Model, tea.Cmd, true). Returning false
// signals "not a slash command" — only used by the very first guard.

package tui

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

	switch cmd {
	case "help":
		return m.handleHelpSlash(args)
	case "quit", "exit", "q":
		m.chat.input = ""
		m.notice = "Goodbye."
		return m.appendSystemMessage("Exiting DFMC — goodbye."), tea.Quit, true
	case "clear":
		return m.handleClearSlash()
	case "export", "save":
		return m.handleExportSlash(cmd, args)
	case "pin", "unpin":
		return m.handlePinSlash(cmd, args)
	case "fork":
		return m.handleForkSlash(args)
	case "retry":
		// Regenerate the most recent assistant reply by resending the latest
		// user message. Body lives in chat_commands_handlers.go.
		return m.handleRetrySlash()
	case "edit":
		// Pull the most recent user message back into the composer so the
		// user can amend it, then press enter to resend. Body lives in
		// chat_commands_handlers.go.
		return m.handleEditSlash()
	case "file", "files":
		return m.handleFilePickerSlash()
	case "plan":
		return m.handlePlanSlash()
	case "code":
		return m.handleCodeSlash()
	case "drive":
		// `/drive` is overloaded: plain task starts a new run; subcommands
		// stop / list / active / resume mirror the CLI surface. Body lives
		// in chat_commands_handlers.go.
		return m.handleDriveSlash(args)
	case "compact":
		// Collapse older transcript entries into a single summary line so
		// long sessions stay scannable. View-layer only — engine memory
		// and conversation history are untouched. Body lives in
		// chat_commands_handlers.go.
		return m.handleCompactSlash(args)
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
		// Two modes:
		//   /tools             — toggle the per-message tool-call strip
		//   /tools list        — print the registered backend tool catalog
		return m.handleToolsSlash(args)
	case "tool":
		return m.handleToolSlash(args, rawArgs)
	case "ls", "read", "grep", "run", "diff", "patch", "undo", "apply":
		return m.runFileCommand(cmd, args)
	case "providers", "provider", "models", "model":
		return m.runProviderCommand(cmd, args)
	case "key", "apikey", "apikeys":
		return m.runKeyCommand(args)
	case "ask":
		return m.handleAskSlash(args)
	case "chat":
		m.chat.input = ""
		m.notice = "Already in chat. Just type your message."
		return m.appendSystemMessage("You're already in the chat tab — type your message and press enter."), nil, true
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
	case "map":
		m.chat.input = ""
		return m.appendSystemMessage(m.codemapSummary()), nil, true
	case "version":
		m.chat.input = ""
		return m.appendSystemMessage(m.versionSummary()), nil, true
	case "doctor", "health":
		// Lightweight health snapshot that covers provider readiness, AST
		// backend, approval gate, hooks, and recent denials in one card.
		// Full `dfmc doctor` does network checks and --fix; this is the
		// in-chat version so users can sanity-check without leaving TUI.
		m.chat.input = ""
		return m.appendSystemMessage(m.describeHealth()), loadStatusCmd(m.eng), true
	case "setup":
		// Provider-config diagnostic + cleaner. Sub-command "clean"
		// strips the providers block from the project config so the
		// user-home preferences win on next reload. Without args,
		// shows the layering snapshot.
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
		// CLI-only commands — surface a friendly pointer instead of
		// the generic "Unknown command" fallback.
		m.chat.input = ""
		m.notice = "/" + cmd + ": run from CLI (not available in TUI)."
		return m.appendSystemMessage("/" + cmd + " is a CLI command. Run: dfmc " + cmd + (func() string {
			if len(args) > 0 {
				return " " + strings.Join(args, " ")
			}
			return ""
		})()), nil, true
	default:
		if suggestion := suggestSlashCommand(cmd); suggestion != "" {
			m.notice = "Unknown /" + cmd + " — try " + suggestion
			return m.appendSystemMessage("Unknown command: /" + cmd + "\nDid you mean " + suggestion + "?  Run /help for the full list."), nil, true
		}
		m.notice = "Unknown chat command: " + raw
		return m.appendSystemMessage("Unknown chat command: " + raw + "\nRun /help for the catalog."), nil, true
	}
}
