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
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/planning"
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
		// Phase K (help unification): /help with no args opens the
		// Ctrl+H overlay — one help surface for ctrl+h / alt+h / /help
		// / /shortcuts / /keys. Per-command help (/help <name>) still
		// prints inline so users can paste a single command's docs out.
		m.chat.input = ""
		if len(args) > 0 {
			return m.appendSystemMessage(renderTUICommandHelp(args[0])), nil, true
		}
		m.ui.showHelpOverlay = true
		m.notice = "Help overlay open — ctrl+h / alt+h / esc to close."
		return m, nil, true
	case "quit", "exit", "q":
		m.chat.input = ""
		m.notice = "Goodbye."
		return m.appendSystemMessage("Exiting DFMC — goodbye."), tea.Quit, true
	case "clear":
		m.chat.input = ""
		m.chat.transcript = nil
		m.chat.scrollback = 0
		m.notice = "Transcript cleared."
		return m.appendSystemMessage("Transcript cleared. Memory and conversation history are untouched."), nil, true
	case "export", "save":
		// Dump the current transcript to a markdown file under the project
		// root (or to the path given as /export path.md). Writes locally,
		// no network, no engine state touched — purely a view-layer save
		// for users who want to share a session out of DFMC.
		//
		// Phase E item 1: /save <n> (where n is a 1-based assistant turn
		// number) saves a single turn instead of the whole transcript —
		// matches the chip line under each assistant bubble.
		m.chat.input = ""
		if len(m.chat.transcript) == 0 {
			m.notice = "Nothing to export yet — start a conversation first."
			return m.appendSystemMessage("Nothing to export yet. Send a message, then /export to save the transcript."), nil, true
		}
		if cmd == "save" && len(args) == 1 {
			if turn, ok := parseAssistantTurnArg(args[0]); ok {
				return m.handleSaveTurnSlash(turn)
			}
		}
		target := strings.TrimSpace(strings.Join(args, " "))
		path, err := m.exportTranscript(target)
		if err != nil {
			m.notice = "Export failed: " + err.Error()
			return m.appendSystemMessage("Export failed: " + err.Error()), nil, true
		}
		m.notice = "Exported transcript → " + path
		return m.appendSystemMessage("▸ Transcript exported → " + path + " (" + fmt.Sprintf("%d lines", len(m.chat.transcript)) + ")."), nil, true
	case "pin", "unpin":
		// Phase E item 1 — pin/unpin <n> toggles the local "anchor" flag
		// on the Nth assistant turn. Pure UI state; engine isn't touched.
		m.chat.input = ""
		if len(args) != 1 {
			m.notice = "Usage: /pin <n> | /unpin <n> — n is the assistant turn number shown in the chip line."
			return m.appendSystemMessage("Usage: /pin <n> | /unpin <n>. The chip under each assistant turn shows its number."), nil, true
		}
		turn, ok := parseAssistantTurnArg(args[0])
		if !ok {
			m.notice = "/" + cmd + ": expected a positive integer turn number."
			return m.appendSystemMessage("/" + cmd + " expects a positive integer turn number — try /pin 3."), nil, true
		}
		return m.handlePinTurnSlash(turn, cmd == "pin")
	case "fork":
		// Phase E item 1 — /fork <n> creates a new conversation branch
		// anchored at the Nth assistant turn. Routes through the engine's
		// ConversationBranchCreate; the brand-new branch becomes active
		// so the next user turn starts from there.
		m.chat.input = ""
		if len(args) < 1 {
			m.notice = "Usage: /fork <n> [name] — n is the assistant turn number to branch from."
			return m.appendSystemMessage("Usage: /fork <n> [name]. Defaults the branch name to fork-from-<n>-<stamp>."), nil, true
		}
		turn, ok := parseAssistantTurnArg(args[0])
		if !ok {
			m.notice = "/fork: expected a positive integer turn number."
			return m.appendSystemMessage("/fork expects a positive integer turn number — try /fork 3."), nil, true
		}
		name := strings.TrimSpace(strings.Join(args[1:], " "))
		return m.handleForkTurnSlash(turn, name)
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
		// Slash-command fallback for the @ mention picker. Same trick as
		// Ctrl+T: insert a leading "@" so the existing mention-picker
		// machinery takes over. Particularly useful for users whose
		// keyboard layout makes Ctrl+T awkward too.
		m.chat.input = ""
		if m.chat.sending {
			m.notice = "Cannot open file picker while a turn is streaming."
			return m.appendSystemMessage("A turn is streaming — esc to cancel first."), nil, true
		}
		m.setChatInput("@")
		m.slashMenu.mention = 0
		m.notice = "File picker open — type to filter, tab/enter inserts, esc cancels."
		if len(m.filesView.entries) == 0 && m.eng != nil {
			return m, loadFilesCmd(m.eng), true
		}
		return m, nil, true
	case "plan":
		// Enter plan mode — agent runs read-only, proposes changes as a
		// plan for the user to approve. Complements /retry and /edit:
		// users who want to survey before mutating finally get a first-
		// class switch instead of relying on prompt discipline.
		m.chat.input = ""
		if m.ui.planMode {
			m.notice = "Already in plan mode — type your question, or /code to exit."
			return m.appendSystemMessage("Plan mode is already ON. Your next prompt will investigate without modifying files. Use /code to exit."), nil, true
		}
		m.ui.planMode = true
		m.notice = "Plan mode ON — investigate-only, no file writes. /code exits."
		return m.appendSystemMessage("▸ Plan mode ON. The agent will investigate with read-only tools (read_file, grep_codebase, ast_query, list_dir, glob, git_status, git_diff) and propose changes as a plan. Type /code to exit plan mode when you're ready to apply."), nil, true
	case "code":
		// Exit plan mode — subsequent prompts are free to mutate.
		m.chat.input = ""
		if !m.ui.planMode {
			m.notice = "Already in code mode — plan mode was not active."
			return m.appendSystemMessage("Not in plan mode. Prompts already allow file modifications."), nil, true
		}
		m.ui.planMode = false
		m.notice = "Plan mode OFF — prompts can now modify files."
		return m.appendSystemMessage("▸ Plan mode OFF. Write/update prompts will now route through mutating tools (apply_patch, edit_file, write_file)."), nil, true
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
		"tasks", "subagents", "workers", "queue",
		"keylog", "coach", "hints", "intent",
		"copy", "yank", "mouse", "select",
		"status", "reload",
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
		m.chat.input = ""
		payload := strings.TrimSpace(strings.Join(args, " "))
		if payload == "" {
			m.notice = "/ask needs a question."
			return m.appendSystemMessage("Usage: /ask <question>\nExample: /ask why is the build slow on Windows?"), nil, true
		}
		next, cmdOut := m.submitChatQuestion(payload, nil)
		return next, cmdOut, true
	case "chat":
		m.chat.input = ""
		m.notice = "Already in chat. Just type your message."
		return m.appendSystemMessage("You're already in the chat tab — type your message and press enter."), nil, true
	case "continue", "resume":
		m.chat.input = ""
		if m.eng == nil || !m.eng.HasParkedAgent() {
			m.notice = "Nothing to resume — no parked agent loop."
			return m.appendSystemMessage("Nothing to resume — the agent isn't paused. /continue only works after a turn pauses at its step or token budget."), nil, true
		}
		note := strings.TrimSpace(strings.Join(args, " "))
		next, cmdOut := m.startChatResume(note)
		return next, cmdOut, true
	case "split":
		m.chat.input = ""
		query := strings.TrimSpace(strings.Join(args, " "))
		if query == "" {
			m.notice = "/split needs a task to decompose."
			return m.appendSystemMessage("Usage: /split <task> — runs the deterministic splitter and shows the subtasks it detects so you can dispatch them individually."), nil, true
		}
		return m.appendSystemMessage(renderSplitPlan(planning.SplitTask(query))), nil, true
	case "btw":
		m.chat.input = ""
		note := strings.TrimSpace(strings.Join(args, " "))
		if note == "" {
			m.notice = "/btw needs a note."
			return m.appendSystemMessage("Usage: /btw <note> — queued text lands as a user message at the next tool-loop step boundary."), nil, true
		}
		if m.eng == nil {
			m.notice = "/btw: engine unavailable."
			return m.appendSystemMessage("/btw: engine is not initialized."), nil, true
		}
		m.eng.QueueAgentNote(note)
		m.chat.pendingNoteCount++
		return m.appendSystemMessage("/btw queued: " + note + "\nIt will land as a user note before the next tool-loop step."), nil, true
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

